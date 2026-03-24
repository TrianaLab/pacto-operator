/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/loader"
	"github.com/trianalab/pacto-operator/internal/metrics"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto-operator/internal/prober"
	"github.com/trianalab/pacto-operator/internal/validator"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/validation"
)

// PactoReconciler reconciles a Pacto object.
type PactoReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Loader   *loader.Loader
}

// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos/finalizers,verbs=update
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactorevisions,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactorevisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *PactoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the Pacto CR
	pacto := &pactov1alpha1.Pacto{}
	if err := r.Get(ctx, req.NamespacedName, pacto); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 2. Reset all derived status fields so no stale data survives.
	//    Fields will be repopulated by each step below.
	r.resetDerivedStatus(pacto)

	// 3. Reject OCI refs with explicit tags (the operator auto-resolves semver)
	ociRef := pacto.Spec.ContractRef.OCI
	if pactov1alpha1.HasExplicitTag(ociRef) {
		msg := fmt.Sprintf("contractRef.oci must not include a tag (got %q); the operator auto-resolves the latest semver version", ociRef)
		return r.failReconciliation(ctx, pacto, msg,
			&pactov1alpha1.ValidationResult{
				Valid:  false,
				Errors: []pactov1alpha1.ValidationIssue{{Path: "spec.contractRef.oci", Message: msg}},
			}, nil)
	}

	// 4. Load the contract
	loadResult, err := r.Loader.Load(ctx, ociRef, pacto.Spec.ContractRef.Inline)
	if err != nil {
		return r.failReconciliation(ctx, pacto, err.Error(),
			&pactov1alpha1.ValidationResult{
				Valid:  false,
				Errors: []pactov1alpha1.ValidationIssue{{Message: err.Error()}},
			}, nil)
	}

	// 5. Structural + cross-field + semantic validation
	contractResult := validation.Validate(loadResult.Contract, loadResult.RawYAML, loadResult.BundleFS)
	pacto.Status.Validation = mapValidationResult(contractResult)

	if len(contractResult.Errors) > 0 {
		msg := formatValidationErrors(contractResult.Errors)
		return r.failReconciliation(ctx, pacto, msg, pacto.Status.Validation, loadResult.Contract)
	}

	r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
		pactov1alpha1.ReasonContractParsed,
		fmt.Sprintf("Contract %s v%s is valid", loadResult.Contract.Service.Name, loadResult.Contract.Service.Version))

	// 6. Populate contract-derived status fields
	pacto.Status.ContractVersion = loadResult.Contract.Service.Version
	r.populateContractStatus(pacto, loadResult)

	// 7. Ensure PactoRevision for current version
	revisionName, revErr := r.ensureRevision(ctx, pacto, loadResult)
	if revErr != nil {
		log.Error(revErr, "Failed to ensure PactoRevision")
	} else {
		pacto.Status.CurrentRevision = revisionName
	}

	if ociRef != "" {
		if syncErr := r.syncAllRevisions(ctx, pacto, ociRef); syncErr != nil {
			log.Error(syncErr, "Failed to sync all revisions")
		}
	}

	// 8. Reference-only: skip runtime validation
	if pacto.IsReference() {
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
			pactov1alpha1.ReasonReferenceOnly,
			fmt.Sprintf("Reference contract %s v%s is valid", loadResult.Contract.Service.Name, loadResult.Contract.Service.Version))
		pacto.Status.Phase = pactov1alpha1.PhaseReference
		pacto.Status.Summary = &pactov1alpha1.CheckSummary{Total: 1, Passed: 1}
		return r.finishReconciliation(ctx, pacto, loadResult.Contract, []validator.Check{
			{Name: pactov1alpha1.ConditionContractValid, Passed: true, Severity: pactov1alpha1.SeverityError},
		})
	}

	// 9. Resolve target
	workloadName, workloadKind := pacto.ResolvedWorkload()
	serviceName := pacto.Spec.Target.ServiceName

	// 10. Observe runtime state
	obs := observer.New(r.Client)
	snapshot, err := obs.Observe(ctx, pacto.Namespace, serviceName, workloadName, workloadKind)
	if err != nil {
		log.Error(err, "Failed to observe runtime state")
		pacto.Status.Phase = pactov1alpha1.PhaseUnknown
		return r.finishReconciliation(ctx, pacto, loadResult.Contract, []validator.Check{
			{Name: pactov1alpha1.ConditionContractValid, Passed: true, Severity: pactov1alpha1.SeverityError},
		})
	}

	// 11. Populate observed runtime into status (for dashboard diff view)
	if snapshot.WorkloadExists {
		pacto.Status.ObservedRuntime = &pactov1alpha1.ObservedRuntime{
			WorkloadKind:                   snapshot.WorkloadKind,
			DeploymentStrategy:             snapshot.DeploymentStrategy,
			PodManagementPolicy:            snapshot.PodManagementPolicy,
			TerminationGracePeriodSeconds:  snapshot.TerminationGracePeriod,
			ContainerImages:                snapshot.ContainerImages,
			HasPVC:                         snapshot.HasPVC,
			HasEmptyDir:                    snapshot.HasEmptyDir,
			HealthProbeInitialDelaySeconds: snapshot.HealthProbeInitialDelay,
		}
	}

	// 12. Validate contract against runtime (includes runtime reconciliation checks)
	hasService := serviceName != ""
	result := validator.Validate(loadResult.Contract, snapshot, hasService)

	// 13. Map validation result → status
	r.applyValidationResult(pacto, result, snapshot, serviceName, workloadName, workloadKind)

	// Collect all checks for metrics: ContractValid + validator checks
	allChecks := make([]validator.Check, 0, len(result.Checks)+3)
	allChecks = append(allChecks, validator.Check{Name: pactov1alpha1.ConditionContractValid, Passed: true, Severity: pactov1alpha1.SeverityError})
	allChecks = append(allChecks, result.Checks...)

	// 14. Probe declared endpoints (only when service exists)
	if snapshot.ServiceExists {
		endpointChecks := r.probeEndpoints(ctx, pacto, loadResult.Contract, serviceName)
		allChecks = append(allChecks, endpointChecks...)
	}

	// 15. Compute final phase including all checks
	pacto.Status.Phase = r.computeFinalPhase(pacto)

	return r.finishReconciliation(ctx, pacto, loadResult.Contract, allChecks)
}

// resetDerivedStatus clears all status fields that are recomputed each reconciliation.
// This prevents stale data from a previous reconciliation from surviving.
func (r *PactoReconciler) resetDerivedStatus(pacto *pactov1alpha1.Pacto) {
	pacto.Status.Phase = ""
	pacto.Status.Summary = nil
	pacto.Status.ContractVersion = ""
	pacto.Status.Contract = nil
	pacto.Status.Validation = nil
	pacto.Status.Resources = nil
	pacto.Status.Ports = nil
	pacto.Status.Endpoints = nil
	pacto.Status.Interfaces = nil
	pacto.Status.Configuration = nil
	pacto.Status.Dependencies = nil
	pacto.Status.Policy = nil
	pacto.Status.Runtime = nil
	pacto.Status.ObservedRuntime = nil
	pacto.Status.Scaling = nil
	pacto.Status.Metadata = nil
	pacto.Status.Conditions = nil
	// Preserve: CurrentRevision (set in step 7), LastReconciledAt/ObservedGeneration (set in finish)
}

// failReconciliation handles the common pattern for contract-level failures:
// sets ContractValid=False, phase=Invalid, summary={1,0,1}, updates status.
func (r *PactoReconciler) failReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, msg string, valResult *pactov1alpha1.ValidationResult, c *contract.Contract) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionFalse,
		pactov1alpha1.ReasonContractInvalid, msg)
	pacto.Status.Phase = pactov1alpha1.PhaseInvalid
	pacto.Status.Summary = &pactov1alpha1.CheckSummary{Total: 1, Failed: 1}
	pacto.Status.Validation = valResult

	now := metav1.Now()
	pacto.Status.LastReconciledAt = &now
	pacto.Status.ObservedGeneration = pacto.Generation

	if statusErr := r.Status().Update(ctx, pacto); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
	}
	r.Recorder.Event(pacto, corev1.EventTypeWarning, "ContractInvalid", msg)

	// Emit metrics so invalid contracts are visible in Prometheus
	if c != nil && c.Service.Name != "" {
		metrics.RecordValidation(pacto.Namespace, c.Service.Name, []validator.Check{
			{Name: pactov1alpha1.ConditionContractValid, Passed: false, Severity: pactov1alpha1.SeverityError},
		})
	}

	return ctrl.Result{RequeueAfter: r.requeueInterval(pacto)}, nil
}

// finishReconciliation sets final metadata, persists status, and emits metrics.
// checks carries the actual validator.Check slice (with Severity) for accurate metric recording.
func (r *PactoReconciler) finishReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, c *contract.Contract, checks []validator.Check) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	now := metav1.Now()
	pacto.Status.LastReconciledAt = &now
	pacto.Status.ObservedGeneration = pacto.Generation

	if statusErr := r.Status().Update(ctx, pacto); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
		return ctrl.Result{}, statusErr
	}

	if pacto.Status.Phase != pactov1alpha1.PhaseHealthy && pacto.Status.Phase != pactov1alpha1.PhaseReference {
		if pacto.Status.Summary != nil {
			r.Recorder.Eventf(pacto, corev1.EventTypeWarning, "ValidationFailed",
				"Phase: %s, %d/%d checks failed", pacto.Status.Phase, pacto.Status.Summary.Failed, pacto.Status.Summary.Total)
		}
	}

	// Emit Prometheus metrics using the actual checks (preserves severity)
	if c != nil && c.Service.Name != "" {
		metrics.RecordValidation(pacto.Namespace, c.Service.Name, checks)
	}

	log.Info("Reconciliation complete", "phase", pacto.Status.Phase)
	return ctrl.Result{RequeueAfter: r.requeueInterval(pacto)}, nil
}

// populateContractStatus extracts structured data from the parsed contract into status fields.
func (r *PactoReconciler) populateContractStatus(pacto *pactov1alpha1.Pacto, lr *loader.LoadResult) {
	c := lr.Contract

	// Contract info
	info := &pactov1alpha1.ContractInfo{
		ServiceName: c.Service.Name,
		Version:     c.Service.Version,
		Owner:       c.Service.Owner,
		ResolvedRef: lr.ResolvedRef,
	}
	if c.Service.Image != nil {
		info.ImageRef = c.Service.Image.Ref
	}
	pacto.Status.Contract = info

	// Interfaces
	for _, iface := range c.Interfaces {
		ii := pactov1alpha1.InterfaceInfo{
			Name:            iface.Name,
			Type:            iface.Type,
			Visibility:      iface.Visibility,
			HasContractFile: iface.Contract != "",
		}
		if iface.Port != nil {
			p := int32(*iface.Port)
			ii.Port = &p
		}
		pacto.Status.Interfaces = append(pacto.Status.Interfaces, ii)
	}

	// Configuration
	if c.Configuration != nil {
		ci := &pactov1alpha1.ConfigurationInfo{
			HasSchema: c.Configuration.Schema != "",
			Ref:       c.Configuration.Ref,
		}
		for k, v := range c.Configuration.Values {
			ci.ValueKeys = append(ci.ValueKeys, k)
			if s, ok := v.(string); ok && strings.HasPrefix(s, "secret://") {
				ci.SecretKeys = append(ci.SecretKeys, k)
			}
		}
		sort.Strings(ci.ValueKeys)
		sort.Strings(ci.SecretKeys)
		pacto.Status.Configuration = ci
	}

	// Dependencies
	for _, dep := range c.Dependencies {
		pacto.Status.Dependencies = append(pacto.Status.Dependencies, pactov1alpha1.DependencyInfo{
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
		})
	}

	// Policy
	if c.Policy != nil {
		pacto.Status.Policy = &pactov1alpha1.PolicyInfo{
			HasSchema: c.Policy.Schema != "",
			Ref:       c.Policy.Ref,
		}
	}

	// Runtime
	if c.Runtime != nil {
		ri := &pactov1alpha1.RuntimeInfo{
			Workload:              c.Runtime.Workload,
			StateType:             c.Runtime.State.Type,
			PersistenceScope:      c.Runtime.State.Persistence.Scope,
			PersistenceDurability: c.Runtime.State.Persistence.Durability,
			DataCriticality:       c.Runtime.State.DataCriticality,
		}
		if c.Runtime.Lifecycle != nil {
			ri.UpgradeStrategy = c.Runtime.Lifecycle.UpgradeStrategy
			if c.Runtime.Lifecycle.GracefulShutdownSeconds != nil {
				gs := int32(*c.Runtime.Lifecycle.GracefulShutdownSeconds)
				ri.GracefulShutdownSeconds = &gs
			}
		}
		if c.Runtime.Health != nil {
			ri.HealthInterface = c.Runtime.Health.Interface
			ri.HealthPath = c.Runtime.Health.Path
			if c.Runtime.Health.InitialDelaySeconds != nil {
				ids := int32(*c.Runtime.Health.InitialDelaySeconds)
				ri.HealthInitialDelaySeconds = &ids
			}
		}
		if c.Runtime.Metrics != nil {
			ri.MetricsInterface = c.Runtime.Metrics.Interface
			ri.MetricsPath = c.Runtime.Metrics.Path
		}
		pacto.Status.Runtime = ri
	}

	// Scaling
	if c.Scaling != nil {
		si := &pactov1alpha1.ScalingInfo{}
		if c.Scaling.Replicas != nil {
			rep := int32(*c.Scaling.Replicas)
			si.Replicas = &rep
		}
		if c.Scaling.Min > 0 {
			min := int32(c.Scaling.Min)
			si.Min = &min
		}
		if c.Scaling.Max > 0 {
			max := int32(c.Scaling.Max)
			si.Max = &max
		}
		pacto.Status.Scaling = si
	}

	// Metadata
	if len(c.Metadata) > 0 {
		pacto.Status.Metadata = make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			pacto.Status.Metadata[k] = fmt.Sprintf("%v", v)
		}
	}
}

// applyValidationResult maps validator output to CRD status fields.
func (r *PactoReconciler) applyValidationResult(
	pacto *pactov1alpha1.Pacto,
	result validator.Result,
	snapshot *observer.RuntimeSnapshot,
	serviceName, workloadName, workloadKind string,
) {
	// Resources
	pacto.Status.Resources = &pactov1alpha1.ResourcesStatus{}
	if serviceName != "" {
		pacto.Status.Resources.Service = &pactov1alpha1.ResourceStatus{
			Name:   serviceName,
			Exists: snapshot.ServiceExists,
		}
	}
	if workloadName != "" {
		pacto.Status.Resources.Workload = &pactov1alpha1.ResourceStatus{
			Name:   workloadName,
			Kind:   workloadKind,
			Exists: snapshot.WorkloadExists,
		}
	}

	// Ports
	if len(result.Ports.Expected) > 0 || len(result.Ports.Observed) > 0 {
		pacto.Status.Ports = &pactov1alpha1.PortStatus{
			Expected:   result.Ports.Expected,
			Observed:   result.Ports.Observed,
			Missing:    result.Ports.Missing,
			Unexpected: result.Ports.Unexpected,
		}
	}

	// Conditions (one per check)
	for _, check := range result.Checks {
		status := metav1.ConditionTrue
		if !check.Passed {
			status = metav1.ConditionFalse
		}
		r.setCondition(pacto, check.Name, status, check.Reason, check.Message)
	}

	// Summary: ContractValid (already passed) + validator checks.
	// Endpoint checks will be added by probeEndpoints.
	var passed, failed int32
	passed = 1 // ContractValid
	for _, check := range result.Checks {
		if check.Passed {
			passed++
		} else {
			failed++
		}
	}
	pacto.Status.Summary = &pactov1alpha1.CheckSummary{
		Total:  int32(len(result.Checks)) + 1,
		Passed: passed,
		Failed: failed,
	}
}

// probeEndpoints checks declared health and metrics endpoints and updates
// status.endpoints, conditions, and summary counts in place.
func (r *PactoReconciler) probeEndpoints(ctx context.Context, pacto *pactov1alpha1.Pacto, c *contract.Contract, serviceName string) []validator.Check {
	if c.Runtime == nil {
		return nil
	}

	// Build interface lookups
	ifaceExists := make(map[string]bool)
	ifacePort := make(map[string]int32)
	for _, iface := range c.Interfaces {
		ifaceExists[iface.Name] = true
		if iface.Port != nil {
			ifacePort[iface.Name] = int32(*iface.Port)
		}
	}

	p := prober.New(0) // default 5s timeout
	var endpoints pactov1alpha1.EndpointsStatus
	var probeChecks []validator.Check

	// Health endpoint
	if c.Runtime.Health != nil && c.Runtime.Health.Interface != "" {
		check, result := r.probeOneEndpoint(ctx, p, probeSpec{
			conditionType: pactov1alpha1.ConditionHealthEndpointValid,
			label:         "health",
			interfaceName: c.Runtime.Health.Interface,
			path:          c.Runtime.Health.Path,
			serviceName:   serviceName,
			namespace:     pacto.Namespace,
			ifaceExists:   ifaceExists,
			ifacePort:     ifacePort,
			requireBody:   false,
		})
		r.applyCheck(pacto, check)
		probeChecks = append(probeChecks, check)
		if result != nil {
			endpoints.Health = result
		}
	}

	// Metrics endpoint
	if c.Runtime.Metrics != nil && c.Runtime.Metrics.Interface != "" {
		check, result := r.probeOneEndpoint(ctx, p, probeSpec{
			conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
			label:         "metrics",
			interfaceName: c.Runtime.Metrics.Interface,
			path:          c.Runtime.Metrics.Path,
			serviceName:   serviceName,
			namespace:     pacto.Namespace,
			ifaceExists:   ifaceExists,
			ifacePort:     ifacePort,
			requireBody:   true,
		})
		r.applyCheck(pacto, check)
		probeChecks = append(probeChecks, check)
		if result != nil {
			endpoints.Metrics = result
		}
	}

	if endpoints.Health != nil || endpoints.Metrics != nil {
		pacto.Status.Endpoints = &endpoints
	}
	return probeChecks
}

// applyCheck adds a check result to conditions and summary.
func (r *PactoReconciler) applyCheck(pacto *pactov1alpha1.Pacto, check validator.Check) {
	status := metav1.ConditionTrue
	if !check.Passed {
		status = metav1.ConditionFalse
	}
	r.setCondition(pacto, check.Name, status, check.Reason, check.Message)

	if pacto.Status.Summary == nil {
		pacto.Status.Summary = &pactov1alpha1.CheckSummary{}
	}
	pacto.Status.Summary.Total++
	if check.Passed {
		pacto.Status.Summary.Passed++
	} else {
		pacto.Status.Summary.Failed++
	}
}

type probeSpec struct {
	conditionType string
	label         string
	interfaceName string
	path          string
	serviceName   string
	namespace     string
	ifaceExists   map[string]bool
	ifacePort     map[string]int32
	requireBody   bool
}

func (r *PactoReconciler) probeOneEndpoint(ctx context.Context, p *prober.Prober, spec probeSpec) (validator.Check, *pactov1alpha1.EndpointCheckResult) {
	// Check interface exists
	if !spec.ifaceExists[spec.interfaceName] {
		return validator.Check{
			Name:    spec.conditionType,
			Passed:  false,
			Reason:  pactov1alpha1.ReasonEndpointInterfaceMissing,
			Message: fmt.Sprintf("Interface %q referenced by %s not found in contract", spec.interfaceName, spec.label),
		}, nil
	}

	// Check interface has a port
	port, hasPort := spec.ifacePort[spec.interfaceName]
	if !hasPort {
		return validator.Check{
			Name:    spec.conditionType,
			Passed:  false,
			Reason:  pactov1alpha1.ReasonEndpointNoPort,
			Message: fmt.Sprintf("Interface %q referenced by %s has no port declared", spec.interfaceName, spec.label),
		}, nil
	}

	url := prober.BuildURL(spec.serviceName, spec.namespace, port, spec.path)

	result := p.Probe(ctx, url)
	epResult := &pactov1alpha1.EndpointCheckResult{
		URL:        url,
		Reachable:  result.Reachable,
		StatusCode: result.StatusCode,
		LatencyMs:  result.LatencyMs,
		Error:      result.Error,
	}

	// Not reachable
	if !result.Reachable {
		return validator.Check{
			Name:    spec.conditionType,
			Passed:  false,
			Reason:  pactov1alpha1.ReasonEndpointConnectionError,
			Message: fmt.Sprintf("%s endpoint %s is unreachable: %s", spec.label, url, result.Error),
		}, epResult
	}

	// Status code check: 2xx or 3xx for health, exactly 200 for metrics
	if spec.requireBody {
		if result.StatusCode != 200 {
			return validator.Check{
				Name:    spec.conditionType,
				Passed:  false,
				Reason:  pactov1alpha1.ReasonEndpointInvalidStatus,
				Message: fmt.Sprintf("%s endpoint %s returned HTTP %d, expected 200", spec.label, url, result.StatusCode),
			}, epResult
		}
		if !result.ContentPresent {
			return validator.Check{
				Name:    spec.conditionType,
				Passed:  false,
				Reason:  pactov1alpha1.ReasonEndpointEmptyResponse,
				Message: fmt.Sprintf("%s endpoint %s returned empty response body", spec.label, url),
			}, epResult
		}
	} else {
		if result.StatusCode < 200 || result.StatusCode >= 400 {
			return validator.Check{
				Name:    spec.conditionType,
				Passed:  false,
				Reason:  pactov1alpha1.ReasonEndpointInvalidStatus,
				Message: fmt.Sprintf("%s endpoint %s returned HTTP %d, expected 2xx/3xx", spec.label, url, result.StatusCode),
			}, epResult
		}
	}

	return validator.Check{
		Name:    spec.conditionType,
		Passed:  true,
		Reason:  pactov1alpha1.ReasonEndpointOK,
		Message: fmt.Sprintf("%s endpoint %s responded with HTTP %d (%dms)", spec.label, url, result.StatusCode, result.LatencyMs),
	}, epResult
}

// computeFinalPhase derives the phase from all conditions set on the CR.
// This is called once, after all checks (validator + probing) are complete.
func (r *PactoReconciler) computeFinalPhase(pacto *pactov1alpha1.Pacto) string {
	hasResourceFailure := false
	hasOtherFailure := false

	for _, cond := range pacto.Status.Conditions {
		if cond.Status == metav1.ConditionTrue {
			continue
		}
		if cond.Type == pactov1alpha1.ConditionServiceExists || cond.Type == pactov1alpha1.ConditionWorkloadExists {
			hasResourceFailure = true
		} else {
			hasOtherFailure = true
		}
	}

	if hasResourceFailure {
		return pactov1alpha1.PhaseInvalid
	}
	if hasOtherFailure {
		return pactov1alpha1.PhaseDegraded
	}
	return pactov1alpha1.PhaseHealthy
}

func (r *PactoReconciler) setCondition(pacto *pactov1alpha1.Pacto, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&pacto.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: pacto.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *PactoReconciler) requeueInterval(pacto *pactov1alpha1.Pacto) time.Duration {
	if pacto.Spec.CheckIntervalSeconds > 0 {
		return time.Duration(pacto.Spec.CheckIntervalSeconds) * time.Second
	}
	return 5 * time.Minute
}

// --- Helpers ---

func mapValidationResult(vr validation.ValidationResult) *pactov1alpha1.ValidationResult {
	result := &pactov1alpha1.ValidationResult{Valid: len(vr.Errors) == 0}
	for _, e := range vr.Errors {
		result.Errors = append(result.Errors, pactov1alpha1.ValidationIssue{
			Code: e.Code, Path: e.Path, Message: e.Message,
		})
	}
	for _, w := range vr.Warnings {
		result.Warnings = append(result.Warnings, pactov1alpha1.ValidationIssue{
			Code: w.Code, Path: w.Path, Message: w.Message,
		})
	}
	return result
}

func formatValidationErrors(errors []contract.ValidationError) string {
	var details []string
	for _, e := range errors {
		if e.Path != "" {
			details = append(details, fmt.Sprintf("%s: %s", e.Path, e.Message))
		} else {
			details = append(details, e.Message)
		}
	}
	return fmt.Sprintf("Contract validation failed: %s", strings.Join(details, "; "))
}

// --- PactoRevision management ---

func (r *PactoReconciler) ensureRevision(ctx context.Context, pacto *pactov1alpha1.Pacto, loadResult *loader.LoadResult) (string, error) {
	log := logf.FromContext(ctx)

	hash := fmt.Sprintf("%x", sha256.Sum256(loadResult.RawYAML))
	shortHash := hash[:7]

	version := loadResult.Contract.Service.Version
	if version == "" {
		version = "unknown"
	}

	sanitizedVersion := strings.ReplaceAll(version, ".", "-")
	revisionName := fmt.Sprintf("%s-%s-%s", pacto.Name, sanitizedVersion, shortHash)
	if len(revisionName) > 253 {
		revisionName = revisionName[:253]
	}

	existing := &pactov1alpha1.PactoRevision{}
	err := r.Get(ctx, client.ObjectKey{Namespace: pacto.Namespace, Name: revisionName}, existing)
	if err == nil {
		if existing.Status.ContractHash == "" || existing.Status.CreatedAt == nil {
			now := metav1.Now()
			existing.Status.Resolved = true
			existing.Status.ContractHash = hash
			if existing.Status.CreatedAt == nil {
				existing.Status.CreatedAt = &now
			}
			if statusErr := r.Status().Update(ctx, existing); statusErr != nil {
				log.V(1).Info("Failed to backfill revision status", "revision", revisionName, "error", statusErr)
			}
		}
		return revisionName, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check for existing revision: %w", err)
	}

	source := pactov1alpha1.RevisionSource{}
	if loadResult.ResolvedRef != "" {
		source.OCI = loadResult.ResolvedRef
	} else if pacto.Spec.ContractRef.OCI != "" {
		source.OCI = pacto.Spec.ContractRef.OCI
	} else {
		source.Inline = true
	}

	now := metav1.Now()
	revision := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionName,
			Namespace: pacto.Namespace,
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       pacto.Name,
				pactov1alpha1.LabelRevisionVersion: version,
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:     version,
			Source:      source,
			PactoRef:    pacto.Name,
			ServiceName: loadResult.Contract.Service.Name,
		},
	}

	if err := ctrl.SetControllerReference(pacto, revision, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, revision); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return revisionName, nil
		}
		return "", fmt.Errorf("failed to create PactoRevision: %w", err)
	}

	revision.Status = pactov1alpha1.PactoRevisionStatus{
		Resolved:     true,
		ContractHash: hash,
		CreatedAt:    &now,
	}
	if statusErr := r.Status().Update(ctx, revision); statusErr != nil {
		log.Error(statusErr, "Failed to update PactoRevision status", "revision", revisionName)
	}

	log.Info("Created PactoRevision", "revision", revisionName, "version", version, "hash", shortHash)
	r.Recorder.Eventf(pacto, corev1.EventTypeNormal, "RevisionCreated", "Created revision %s for contract v%s", revisionName, version)

	return revisionName, nil
}

func (r *PactoReconciler) syncAllRevisions(ctx context.Context, pacto *pactov1alpha1.Pacto, baseRef string) error {
	log := logf.FromContext(ctx)

	tags, err := r.Loader.ListTags(ctx, baseRef)
	if err != nil {
		return fmt.Errorf("failed to list tags: %w", err)
	}

	for _, tag := range tags {
		taggedRef := strings.TrimPrefix(baseRef, "oci://")
		if idx := strings.LastIndex(taggedRef, ":"); idx > strings.LastIndex(taggedRef, "/") {
			taggedRef = taggedRef[:idx]
		}
		taggedRef = taggedRef + ":" + tag

		revList := &pactov1alpha1.PactoRevisionList{}
		if listErr := r.List(ctx, revList,
			client.InNamespace(pacto.Namespace),
			client.MatchingLabels{
				pactov1alpha1.LabelPactoName:       pacto.Name,
				pactov1alpha1.LabelRevisionVersion: tag,
			},
		); listErr != nil {
			log.V(1).Info("Failed to list revisions for tag", "tag", tag, "error", listErr)
			continue
		} else if len(revList.Items) > 0 {
			continue
		}

		loadResult, loadErr := r.Loader.Load(ctx, taggedRef, "")
		if loadErr != nil {
			log.V(1).Info("Skipping tag: failed to load", "tag", tag, "error", loadErr)
			continue
		}

		revName, revErr := r.ensureRevision(ctx, pacto, loadResult)
		if revErr != nil {
			log.V(1).Info("Skipping tag: failed to create revision", "tag", tag, "error", revErr)
			continue
		}
		log.V(1).Info("Synced revision for tag", "tag", tag, "revision", revName)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PactoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pactov1alpha1.Pacto{}).
		Owns(&pactov1alpha1.PactoRevision{}).
		Watches(&corev1.Service{}, enqueueForTarget(mgr.GetClient())).
		Watches(&appsv1.Deployment{}, enqueueForTarget(mgr.GetClient())).
		Watches(&appsv1.StatefulSet{}, enqueueForTarget(mgr.GetClient())).
		Watches(&appsv1.ReplicaSet{}, enqueueForTarget(mgr.GetClient())).
		Watches(&batchv1.Job{}, enqueueForTarget(mgr.GetClient())).
		Watches(&batchv1.CronJob{}, enqueueForTarget(mgr.GetClient())).
		Named("pacto").
		Complete(r)
}
