/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContractRef specifies where to find the Pacto contract.
type ContractRef struct {
	// OCI is the OCI registry reference for the contract bundle (without tag).
	// The operator automatically resolves the latest semver tag from the registry.
	// Example: ghcr.io/org/service-pacto
	// +optional
	OCI string `json:"oci,omitempty"`

	// Inline allows specifying the contract YAML directly (for testing/dev).
	// +optional
	Inline string `json:"inline,omitempty"`
}

// WorkloadRef identifies a workload resource by name and kind.
type WorkloadRef struct {
	// Name of the workload resource.
	// +required
	Name string `json:"name"`

	// Kind of the workload resource.
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;ReplicaSet
	// +kubebuilder:default=Deployment
	// +optional
	Kind string `json:"kind,omitempty"`
}

// TargetRef specifies which Kubernetes resources to observe.
type TargetRef struct {
	// ServiceName is the name of the Kubernetes Service to observe.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// WorkloadRef identifies the workload (Deployment, StatefulSet, or ReplicaSet).
	// If omitted, defaults to name=serviceName, kind=Deployment.
	// +optional
	WorkloadRef *WorkloadRef `json:"workloadRef,omitempty"`
}

// PactoSpec defines the desired state of Pacto.
type PactoSpec struct {
	// ContractRef specifies where to find the Pacto contract.
	// +required
	ContractRef ContractRef `json:"contractRef"`

	// Target specifies which Kubernetes resources to observe.
	// When omitted, the Pacto acts as a reference-only contract (no runtime validation).
	// +optional
	Target TargetRef `json:"target,omitempty"`

	// CheckIntervalSeconds controls how often the reconciler re-checks compliance.
	// Defaults to 300 (5 minutes).
	// +optional
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=30
	CheckIntervalSeconds int32 `json:"checkIntervalSeconds,omitempty"`
}

// --- Status types (designed as a stable, structured API for external consumers) ---

// ResourceStatus describes the observed state of a Kubernetes resource.
type ResourceStatus struct {
	// Name of the resource.
	Name string `json:"name"`

	// Kind of the resource (only set for workloads).
	// +optional
	Kind string `json:"kind,omitempty"`

	// Exists indicates whether the resource was found in the cluster.
	Exists bool `json:"exists"`
}

// PortStatus describes the port comparison between contract and runtime.
type PortStatus struct {
	// Expected lists ports declared in the contract.
	Expected []int32 `json:"expected,omitempty"`

	// Observed lists ports found on the Kubernetes Service.
	Observed []int32 `json:"observed,omitempty"`

	// Missing lists contract ports not found on the Service.
	Missing []int32 `json:"missing,omitempty"`

	// Unexpected lists Service ports not declared in the contract.
	Unexpected []int32 `json:"unexpected,omitempty"`
}

// CheckSummary provides precomputed check counts.
type CheckSummary struct {
	// Total is the number of checks performed.
	Total int32 `json:"total"`

	// Passed is the number of checks that passed.
	Passed int32 `json:"passed"`

	// Failed is the number of checks that failed.
	Failed int32 `json:"failed"`
}

// ResourcesStatus groups the status of target resources.
type ResourcesStatus struct {
	// Service describes the target Service.
	// +optional
	Service *ResourceStatus `json:"service,omitempty"`

	// Workload describes the target workload (Deployment/StatefulSet/ReplicaSet).
	// +optional
	Workload *ResourceStatus `json:"workload,omitempty"`
}

// ContractInfo exposes parsed contract metadata.
type ContractInfo struct {
	// ServiceName is the service name declared in the contract.
	ServiceName string `json:"serviceName"`

	// Version is the semver version from the contract.
	Version string `json:"version"`

	// Owner is the team/individual owning this service.
	// +optional
	Owner string `json:"owner,omitempty"`

	// ImageRef is the container image reference from the contract.
	// +optional
	ImageRef string `json:"imageRef,omitempty"`

	// ResolvedRef is the fully-resolved OCI reference (with tag/digest).
	// Empty for inline contracts.
	// +optional
	ResolvedRef string `json:"resolvedRef,omitempty"`
}

// InterfaceInfo describes a single interface declared in the contract.
type InterfaceInfo struct {
	// Name is the interface name.
	Name string `json:"name"`

	// Type is the interface type: http, grpc, or event.
	// +kubebuilder:validation:Enum=http;grpc;event
	Type string `json:"type"`

	// Port is the declared port number.
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Visibility is the declared visibility: public or internal.
	// +optional
	Visibility string `json:"visibility,omitempty"`

	// HasContractFile indicates whether a contract file (OpenAPI, protobuf, AsyncAPI)
	// is present in the bundle for this interface.
	HasContractFile bool `json:"hasContractFile"`
}

// ConfigurationInfo describes the contract's configuration section.
type ConfigurationInfo struct {
	// HasSchema indicates whether a JSON Schema file is bundled.
	HasSchema bool `json:"hasSchema"`

	// Ref is the external OCI reference for the configuration schema, if used.
	// +optional
	Ref string `json:"ref,omitempty"`

	// ValueKeys lists the declared configuration value keys.
	// +optional
	ValueKeys []string `json:"valueKeys,omitempty"`

	// SecretKeys lists configuration keys whose values reference secrets.
	// +optional
	SecretKeys []string `json:"secretKeys,omitempty"`
}

// DependencyInfo describes a declared dependency.
type DependencyInfo struct {
	// Ref is the dependency reference (OCI URI).
	Ref string `json:"ref"`

	// Required indicates whether this dependency is mandatory.
	Required bool `json:"required"`

	// Compatibility is the semver constraint for the dependency.
	// +optional
	Compatibility string `json:"compatibility,omitempty"`
}

// PolicyInfo describes the contract's policy section.
type PolicyInfo struct {
	// HasSchema indicates whether a policy schema file is bundled.
	HasSchema bool `json:"hasSchema"`

	// Ref is the external OCI reference for the policy schema, if used.
	// +optional
	Ref string `json:"ref,omitempty"`
}

// RuntimeInfo describes the contract's runtime section.
type RuntimeInfo struct {
	// Workload is the workload type: service, job, or scheduled.
	// +optional
	Workload string `json:"workload,omitempty"`

	// StateType is the state semantics: stateless, stateful, or hybrid.
	// +optional
	StateType string `json:"stateType,omitempty"`

	// PersistenceScope is the persistence scope: local or shared.
	// +optional
	PersistenceScope string `json:"persistenceScope,omitempty"`

	// PersistenceDurability is the durability: ephemeral or persistent.
	// +optional
	PersistenceDurability string `json:"persistenceDurability,omitempty"`

	// DataCriticality is the data criticality level: low, medium, or high.
	// +optional
	DataCriticality string `json:"dataCriticality,omitempty"`

	// UpgradeStrategy is the declared upgrade strategy: rolling, recreate, or ordered.
	// +optional
	UpgradeStrategy string `json:"upgradeStrategy,omitempty"`

	// GracefulShutdownSeconds is the declared graceful shutdown period.
	// +optional
	GracefulShutdownSeconds *int32 `json:"gracefulShutdownSeconds,omitempty"`

	// HealthInterface is the interface used for health checks.
	// +optional
	HealthInterface string `json:"healthInterface,omitempty"`

	// HealthPath is the HTTP path for health checks.
	// +optional
	HealthPath string `json:"healthPath,omitempty"`

	// MetricsInterface is the interface used for metrics.
	// +optional
	MetricsInterface string `json:"metricsInterface,omitempty"`

	// MetricsPath is the HTTP path for metrics.
	// +optional
	MetricsPath string `json:"metricsPath,omitempty"`

	// HealthInitialDelaySeconds is the declared initial delay before health checks start.
	// +optional
	HealthInitialDelaySeconds *int32 `json:"healthInitialDelaySeconds,omitempty"`
}

// ObservedRuntime describes the actual runtime state observed from the cluster.
// This complements RuntimeInfo (contract-declared) to enable contract-vs-runtime comparison.
type ObservedRuntime struct {
	// WorkloadKind is the actual Kubernetes resource kind (Deployment, StatefulSet, Job, CronJob).
	// +optional
	WorkloadKind string `json:"workloadKind,omitempty"`

	// DeploymentStrategy is the observed strategy (RollingUpdate, Recreate). Empty for non-Deployments.
	// +optional
	DeploymentStrategy string `json:"deploymentStrategy,omitempty"`

	// PodManagementPolicy is the observed pod management policy (OrderedReady, Parallel). Empty for non-StatefulSets.
	// +optional
	PodManagementPolicy string `json:"podManagementPolicy,omitempty"`

	// TerminationGracePeriodSeconds is the observed terminationGracePeriodSeconds from the pod spec.
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// ContainerImages lists the container images from the pod spec.
	// +optional
	ContainerImages []string `json:"containerImages,omitempty"`

	// HasPVC indicates whether the workload uses PersistentVolumeClaims.
	HasPVC bool `json:"hasPVC"`

	// HasEmptyDir indicates whether the workload uses emptyDir volumes.
	HasEmptyDir bool `json:"hasEmptyDir"`

	// HealthProbeInitialDelaySeconds is the observed initialDelaySeconds from the first container's probe.
	// +optional
	HealthProbeInitialDelaySeconds *int32 `json:"healthProbeInitialDelaySeconds,omitempty"`
}

// ScalingInfo describes the contract's scaling section.
type ScalingInfo struct {
	// Replicas is the exact replica count (mutually exclusive with Min/Max).
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Min is the minimum replica count for autoscaling.
	// +optional
	Min *int32 `json:"min,omitempty"`

	// Max is the maximum replica count for autoscaling.
	// +optional
	Max *int32 `json:"max,omitempty"`
}

// EndpointCheckResult describes the result of probing a single HTTP endpoint.
type EndpointCheckResult struct {
	// URL is the fully-constructed endpoint URL that was probed.
	URL string `json:"url"`

	// Reachable indicates whether the HTTP request completed (no connection error).
	Reachable bool `json:"reachable"`

	// StatusCode is the HTTP status code returned. Zero if unreachable.
	// +optional
	StatusCode int32 `json:"statusCode,omitempty"`

	// LatencyMs is the request round-trip time in milliseconds.
	// +optional
	LatencyMs int64 `json:"latencyMs,omitempty"`

	// Error is the error message if the probe failed. Empty on success.
	// +optional
	Error string `json:"error,omitempty"`
}

// EndpointsStatus describes the runtime probe results for declared endpoints.
type EndpointsStatus struct {
	// Health describes the result of probing the declared health endpoint.
	// +optional
	Health *EndpointCheckResult `json:"health,omitempty"`

	// Metrics describes the result of probing the declared metrics endpoint.
	// +optional
	Metrics *EndpointCheckResult `json:"metrics,omitempty"`
}

// ValidationIssue describes a single validation error or warning.
type ValidationIssue struct {
	// Code is a machine-readable error code.
	// +optional
	Code string `json:"code,omitempty"`

	// Path is the JSON path to the invalid field.
	// +optional
	Path string `json:"path,omitempty"`

	// Message is a human-readable description of the issue.
	Message string `json:"message"`
}

// ValidationResult describes the structural validation outcome of the contract.
type ValidationResult struct {
	// Valid indicates whether the contract passed structural validation.
	Valid bool `json:"valid"`

	// Errors lists structural validation errors.
	// +optional
	Errors []ValidationIssue `json:"errors,omitempty"`

	// Warnings lists structural validation warnings.
	// +optional
	Warnings []ValidationIssue `json:"warnings,omitempty"`
}

// PactoStatus defines the observed state of Pacto.
// All contract data is exposed as structured fields so external consumers
// can read the CR status directly without parsing contracts themselves.
type PactoStatus struct {
	// ContractStatus is the high-level contract compliance state.
	// This reflects contract validation/compliance and is NOT runtime health.
	// +kubebuilder:validation:Enum=Compliant;Warning;NonCompliant;Reference;Unknown
	// +optional
	ContractStatus string `json:"contractStatus,omitempty"`

	// Summary provides precomputed check counts.
	// +optional
	Summary *CheckSummary `json:"summary,omitempty"`

	// ContractVersion is the version from the parsed contract.
	// Kept for backward compatibility and simple access via JSONPath.
	// +optional
	ContractVersion string `json:"contractVersion,omitempty"`

	// Contract exposes parsed contract metadata.
	// +optional
	Contract *ContractInfo `json:"contract,omitempty"`

	// Validation describes the structural validation outcome of the contract.
	// +optional
	Validation *ValidationResult `json:"validation,omitempty"`

	// Resources describes the existence of target Kubernetes resources.
	// +optional
	Resources *ResourcesStatus `json:"resources,omitempty"`

	// Ports describes the port comparison between contract and runtime.
	// +optional
	Ports *PortStatus `json:"ports,omitempty"`

	// Endpoints describes the runtime probe results for declared health and metrics endpoints.
	// Only populated when the service exists and the contract declares health/metrics.
	// +optional
	Endpoints *EndpointsStatus `json:"endpoints,omitempty"`

	// Interfaces lists the parsed interfaces from the contract.
	// +optional
	Interfaces []InterfaceInfo `json:"interfaces,omitempty"`

	// Configuration describes the contract's configuration section.
	// +optional
	Configuration *ConfigurationInfo `json:"configuration,omitempty"`

	// Dependencies lists the declared dependencies from the contract.
	// +optional
	Dependencies []DependencyInfo `json:"dependencies,omitempty"`

	// Policy describes the contract's policy section.
	// +optional
	Policy *PolicyInfo `json:"policy,omitempty"`

	// Runtime describes the contract's runtime section (declared).
	// +optional
	Runtime *RuntimeInfo `json:"runtime,omitempty"`

	// ObservedRuntime describes the actual runtime state observed from the cluster.
	// Only populated when a target workload exists.
	// +optional
	ObservedRuntime *ObservedRuntime `json:"observedRuntime,omitempty"`

	// Scaling describes the contract's scaling section.
	// +optional
	Scaling *ScalingInfo `json:"scaling,omitempty"`

	// Metadata contains arbitrary key-value pairs from the contract's metadata section.
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`

	// Conditions represent individual validation checks.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CurrentRevision is the name of the active PactoRevision.
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`

	// LastReconciledAt is when the last reconciliation completed.
	// +optional
	LastReconciledAt *metav1.Time `json:"lastReconciledAt,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.contractStatus`
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.spec.target.serviceName`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.contractVersion`
// +kubebuilder:printcolumn:name="Passed",type=integer,JSONPath=`.status.summary.passed`
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.summary.failed`
// +kubebuilder:printcolumn:name="Last Reconciled",type=date,JSONPath=`.status.lastReconciledAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Pacto is the Schema for the pactos API.
type Pacto struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Pacto.
	// +required
	Spec PactoSpec `json:"spec"`

	// status defines the observed state of Pacto.
	// +optional
	Status PactoStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PactoList contains a list of Pacto.
type PactoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Pacto `json:"items"`
}

// IsReference returns true when the Pacto has no runtime target (reference-only contract).
func (p *Pacto) IsReference() bool {
	return p.Spec.Target.ServiceName == "" && p.Spec.Target.WorkloadRef == nil
}

// ResolvedWorkload returns the effective workload name and kind,
// applying defaults when WorkloadRef is not explicitly set.
func (p *Pacto) ResolvedWorkload() (name, kind string) {
	if p.Spec.Target.WorkloadRef != nil {
		kind = p.Spec.Target.WorkloadRef.Kind
		if kind == "" {
			kind = "Deployment"
		}
		return p.Spec.Target.WorkloadRef.Name, kind
	}
	if p.Spec.Target.ServiceName != "" {
		return p.Spec.Target.ServiceName, "Deployment"
	}
	return "", ""
}

// HasExplicitTag reports whether an OCI reference includes an explicit tag
// (e.g. ":1.0.0" after the last "/" path segment). Digests ("@sha256:...")
// and registry ports ("registry:5000/...") are not considered tags.
func HasExplicitTag(ref string) bool {
	if ref == "" {
		return false
	}
	ref = strings.TrimPrefix(ref, "oci://")
	// Digests are allowed
	if strings.Contains(ref, "@") {
		return false
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	return lastColon > lastSlash
}

func init() {
	SchemeBuilder.Register(&Pacto{}, &PactoList{})
}
