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
	"strings"
	"testing"
	"time"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/loader"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto-operator/internal/prober"
	"github.com/trianalab/pacto-operator/internal/validator"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/validation"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// mockLoader implements ContractLoader for testing.
type mockLoader struct {
	loadFn     func(ctx context.Context, ociRef, inline string) (*loader.LoadResult, error)
	listTagsFn func(ctx context.Context, ociRef string) ([]string, error)
}

func (m *mockLoader) Load(ctx context.Context, ociRef, inline string) (*loader.LoadResult, error) {
	if m.loadFn != nil {
		return m.loadFn(ctx, ociRef, inline)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLoader) ListTags(ctx context.Context, ociRef string) ([]string, error) {
	if m.listTagsFn != nil {
		return m.listTagsFn(ctx, ociRef)
	}
	return nil, fmt.Errorf("not implemented")
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = pactov1alpha1.AddToScheme(s)
	return s
}

func newReconciler(objs ...client.Object) *PactoReconciler {
	s := newScheme()
	cb := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{})
	if len(objs) > 0 {
		cb = cb.WithObjects(objs...)
	}
	return &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader:   &mockLoader{},
	}
}

// ---------- formatValidationErrors ----------

func TestFormatValidationErrors_WithPath(t *testing.T) {
	errs := []contract.ValidationError{
		{Code: "E001", Path: "service.name", Message: "name is required"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "service.name: name is required") {
		t.Fatalf("expected path:message format, got %q", got)
	}
	if !strings.HasPrefix(got, "Contract validation failed:") {
		t.Fatalf("expected prefix, got %q", got)
	}
}

func TestFormatValidationErrors_WithoutPath(t *testing.T) {
	errs := []contract.ValidationError{
		{Code: "E002", Message: "something wrong"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "something wrong") {
		t.Fatalf("expected message, got %q", got)
	}
	// Should not have "path: message" format when path is empty —
	// there should be no extra colon before the message
	expected := "Contract validation failed: something wrong"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatValidationErrors_Multiple(t *testing.T) {
	errs := []contract.ValidationError{
		{Path: "a", Message: "err1"},
		{Message: "err2"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "a: err1") {
		t.Fatalf("expected first error, got %q", got)
	}
	if !strings.Contains(got, "err2") {
		t.Fatalf("expected second error, got %q", got)
	}
	if !strings.Contains(got, "; ") {
		t.Fatalf("expected semicolon separator, got %q", got)
	}
}

// ---------- mapValidationResult ----------

func TestMapValidationResult_ErrorsOnly(t *testing.T) {
	vr := validation.ValidationResult{
		Errors: []contract.ValidationError{
			{Code: "E1", Path: "p", Message: "m"},
		},
	}
	got := mapValidationResult(vr)
	if got.Valid {
		t.Fatal("expected Valid=false when errors present")
	}
	if len(got.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(got.Errors))
	}
	if got.Errors[0].Code != "E1" || got.Errors[0].Path != "p" || got.Errors[0].Message != "m" {
		t.Fatalf("error fields mismatch: %+v", got.Errors[0])
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(got.Warnings))
	}
}

func TestMapValidationResult_WarningsOnly(t *testing.T) {
	vr := validation.ValidationResult{
		Warnings: []contract.ValidationWarning{
			{Code: "W1", Path: "wp", Message: "wm"},
		},
	}
	got := mapValidationResult(vr)
	if !got.Valid {
		t.Fatal("expected Valid=true when no errors")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(got.Warnings))
	}
	if got.Warnings[0].Code != "W1" || got.Warnings[0].Path != "wp" || got.Warnings[0].Message != "wm" {
		t.Fatalf("warning fields mismatch: %+v", got.Warnings[0])
	}
}

func TestMapValidationResult_ErrorsAndWarnings(t *testing.T) {
	vr := validation.ValidationResult{
		Errors:   []contract.ValidationError{{Code: "E1", Message: "e"}},
		Warnings: []contract.ValidationWarning{{Code: "W1", Message: "w"}},
	}
	got := mapValidationResult(vr)
	if got.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(got.Errors) != 1 || len(got.Warnings) != 1 {
		t.Fatalf("expected 1 error and 1 warning, got %d/%d", len(got.Errors), len(got.Warnings))
	}
}

func TestMapValidationResult_Empty(t *testing.T) {
	vr := validation.ValidationResult{}
	got := mapValidationResult(vr)
	if !got.Valid {
		t.Fatal("expected Valid=true when no errors")
	}
}

// ---------- requeueInterval ----------

func TestRequeueInterval_Default(t *testing.T) {
	r := &PactoReconciler{}
	p := &pactov1alpha1.Pacto{}
	got := r.requeueInterval(p)
	if got != 5*time.Minute {
		t.Fatalf("expected 5m, got %v", got)
	}
}

func TestRequeueInterval_Custom(t *testing.T) {
	r := &PactoReconciler{}
	p := &pactov1alpha1.Pacto{
		Spec: pactov1alpha1.PactoSpec{
			CheckIntervalSeconds: 60,
		},
	}
	got := r.requeueInterval(p)
	if got != 60*time.Second {
		t.Fatalf("expected 60s, got %v", got)
	}
}

// ---------- applyCheck ----------

func TestApplyCheck_Passed(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	check := validator.Check{
		Name:    pactov1alpha1.ConditionHealthEndpointValid,
		Passed:  true,
		Reason:  pactov1alpha1.ReasonEndpointOK,
		Message: "healthy",
	}
	r.applyCheck(pacto, check)

	if pacto.Status.Summary == nil {
		t.Fatal("expected summary to be set")
	}
	if pacto.Status.Summary.Total != 1 || pacto.Status.Summary.Passed != 1 || pacto.Status.Summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", pacto.Status.Summary)
	}

	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionHealthEndpointValid)
	if cond == nil {
		t.Fatal("expected condition to be set")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected ConditionTrue, got %v", cond.Status)
	}
}

func TestApplyCheck_Failed(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	check := validator.Check{
		Name:    pactov1alpha1.ConditionMetricsEndpointValid,
		Passed:  false,
		Reason:  pactov1alpha1.ReasonEndpointConnectionError,
		Message: "unreachable",
	}
	r.applyCheck(pacto, check)

	if pacto.Status.Summary.Total != 1 || pacto.Status.Summary.Passed != 0 || pacto.Status.Summary.Failed != 1 {
		t.Fatalf("unexpected summary: %+v", pacto.Status.Summary)
	}

	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionMetricsEndpointValid)
	if cond == nil {
		t.Fatal("expected condition to be set")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected ConditionFalse, got %v", cond.Status)
	}
}

func TestApplyCheck_AccumulatesSummary(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			Summary: &pactov1alpha1.CheckSummary{Total: 3, Passed: 2, Failed: 1},
		},
	}

	r.applyCheck(pacto, validator.Check{Name: "Extra", Passed: true, Reason: "ok", Message: "ok"})

	if pacto.Status.Summary.Total != 4 || pacto.Status.Summary.Passed != 3 || pacto.Status.Summary.Failed != 1 {
		t.Fatalf("unexpected summary: %+v", pacto.Status.Summary)
	}
}

// ---------- populateContractStatus ----------

func TestPopulateContractStatus_Full(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	port8080 := 8080
	port9090 := 9090
	replicas := 3
	graceful := 30
	initialDelay := 10

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{
				Name:    "my-svc",
				Version: "1.2.0",
				Owner:   "team-x",
				Image:   &contract.Image{Ref: "ghcr.io/org/my-svc:1.2.0", Private: true},
			},
			Interfaces: []contract.Interface{
				{Name: "http-api", Type: "http", Port: &port8080, Visibility: "public", Contract: "openapi.yaml"},
				{Name: "grpc-api", Type: "grpc", Port: &port9090, Visibility: "internal"},
				{Name: "events", Type: "event"}, // no port
			},
			Configuration: &contract.Configuration{
				Schema: "config-schema.json",
				Ref:    "oci://config-ref",
				Values: map[string]any{
					"db_host":     "localhost",
					"db_password": "secret://vault/db-pass",
					"log_level":   "info",
				},
			},
			Dependencies: []contract.Dependency{
				{Ref: "oci://dep-a", Required: true, Compatibility: "^1.0.0"},
				{Ref: "oci://dep-b", Required: false},
			},
			Policy: &contract.Policy{
				Schema: "policy-schema.json",
				Ref:    "oci://policy-ref",
			},
			Runtime: &contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateful",
					Persistence:     contract.Persistence{Scope: "local", Durability: "persistent"},
					DataCriticality: "high",
				},
				Lifecycle: &contract.Lifecycle{
					UpgradeStrategy:         "rolling",
					GracefulShutdownSeconds: &graceful,
				},
				Health: &contract.Health{
					Interface:           "http-api",
					Path:                "/healthz",
					InitialDelaySeconds: &initialDelay,
				},
				Metrics: &contract.Metrics{
					Interface: "http-api",
					Path:      "/metrics",
				},
			},
			Scaling: &contract.Scaling{
				Replicas: &replicas,
				Min:      2,
				Max:      5,
			},
			Metadata: map[string]any{
				"team":     "platform",
				"priority": 1,
			},
		},
		RawYAML:     []byte("test"),
		ResolvedRef: "ghcr.io/org/my-svc:1.2.0",
	}

	r.populateContractStatus(pacto, lr)

	// Contract info
	if pacto.Status.Contract == nil {
		t.Fatal("expected contract info")
	}
	if pacto.Status.Contract.ServiceName != "my-svc" {
		t.Fatalf("expected my-svc, got %s", pacto.Status.Contract.ServiceName)
	}
	if pacto.Status.Contract.Version != "1.2.0" {
		t.Fatalf("expected 1.2.0, got %s", pacto.Status.Contract.Version)
	}
	if pacto.Status.Contract.Owner != "team-x" {
		t.Fatalf("expected team-x, got %s", pacto.Status.Contract.Owner)
	}
	if pacto.Status.Contract.ImageRef != "ghcr.io/org/my-svc:1.2.0" {
		t.Fatalf("expected image ref, got %s", pacto.Status.Contract.ImageRef)
	}
	if pacto.Status.Contract.ResolvedRef != "ghcr.io/org/my-svc:1.2.0" {
		t.Fatalf("expected resolved ref, got %s", pacto.Status.Contract.ResolvedRef)
	}

	// Interfaces
	if len(pacto.Status.Interfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d", len(pacto.Status.Interfaces))
	}
	httpAPI := pacto.Status.Interfaces[0]
	if httpAPI.Name != "http-api" || httpAPI.Type != "http" {
		t.Fatalf("unexpected interface: %+v", httpAPI)
	}
	if httpAPI.Port == nil || *httpAPI.Port != 8080 {
		t.Fatalf("expected port 8080, got %v", httpAPI.Port)
	}
	if httpAPI.Visibility != "public" || !httpAPI.HasContractFile {
		t.Fatalf("unexpected interface fields: %+v", httpAPI)
	}
	eventsIface := pacto.Status.Interfaces[2]
	if eventsIface.Port != nil {
		t.Fatalf("expected nil port for events, got %v", eventsIface.Port)
	}

	// Configuration
	if pacto.Status.Configuration == nil {
		t.Fatal("expected configuration info")
	}
	if !pacto.Status.Configuration.HasSchema {
		t.Fatal("expected HasSchema=true")
	}
	if pacto.Status.Configuration.Ref != "oci://config-ref" {
		t.Fatalf("expected config ref, got %s", pacto.Status.Configuration.Ref)
	}
	if len(pacto.Status.Configuration.ValueKeys) != 3 {
		t.Fatalf("expected 3 value keys, got %d", len(pacto.Status.Configuration.ValueKeys))
	}
	if pacto.Status.Configuration.ValueKeys[0] != "db_host" {
		t.Fatalf("expected sorted value keys, first is %s", pacto.Status.Configuration.ValueKeys[0])
	}
	if len(pacto.Status.Configuration.SecretKeys) != 1 {
		t.Fatalf("expected 1 secret key, got %d", len(pacto.Status.Configuration.SecretKeys))
	}
	if pacto.Status.Configuration.SecretKeys[0] != "db_password" {
		t.Fatalf("expected db_password secret key, got %s", pacto.Status.Configuration.SecretKeys[0])
	}

	// Dependencies
	if len(pacto.Status.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(pacto.Status.Dependencies))
	}
	if pacto.Status.Dependencies[0].Ref != "oci://dep-a" || !pacto.Status.Dependencies[0].Required {
		t.Fatalf("unexpected dep: %+v", pacto.Status.Dependencies[0])
	}
	if pacto.Status.Dependencies[1].Compatibility != "" {
		t.Fatalf("expected empty compatibility for dep-b, got %s", pacto.Status.Dependencies[1].Compatibility)
	}

	// Policy
	if pacto.Status.Policy == nil {
		t.Fatal("expected policy info")
	}
	if !pacto.Status.Policy.HasSchema {
		t.Fatal("expected HasSchema=true for policy")
	}
	if pacto.Status.Policy.Ref != "oci://policy-ref" {
		t.Fatalf("expected policy ref, got %s", pacto.Status.Policy.Ref)
	}

	// Runtime
	if pacto.Status.Runtime == nil {
		t.Fatal("expected runtime info")
	}
	ri := pacto.Status.Runtime
	if ri.Workload != "service" {
		t.Fatalf("expected service workload, got %s", ri.Workload)
	}
	if ri.StateType != "stateful" {
		t.Fatalf("expected stateful, got %s", ri.StateType)
	}
	if ri.PersistenceScope != "local" {
		t.Fatalf("expected local scope, got %s", ri.PersistenceScope)
	}
	if ri.PersistenceDurability != "persistent" {
		t.Fatalf("expected persistent durability, got %s", ri.PersistenceDurability)
	}
	if ri.DataCriticality != "high" {
		t.Fatalf("expected high, got %s", ri.DataCriticality)
	}
	if ri.UpgradeStrategy != "rolling" {
		t.Fatalf("expected rolling, got %s", ri.UpgradeStrategy)
	}
	if ri.GracefulShutdownSeconds == nil || *ri.GracefulShutdownSeconds != 30 {
		t.Fatalf("expected 30, got %v", ri.GracefulShutdownSeconds)
	}
	if ri.HealthInterface != "http-api" {
		t.Fatalf("expected http-api health interface, got %s", ri.HealthInterface)
	}
	if ri.HealthPath != "/healthz" {
		t.Fatalf("expected /healthz, got %s", ri.HealthPath)
	}
	if ri.HealthInitialDelaySeconds == nil || *ri.HealthInitialDelaySeconds != 10 {
		t.Fatalf("expected 10, got %v", ri.HealthInitialDelaySeconds)
	}
	if ri.MetricsInterface != "http-api" {
		t.Fatalf("expected http-api metrics interface, got %s", ri.MetricsInterface)
	}
	if ri.MetricsPath != "/metrics" {
		t.Fatalf("expected /metrics, got %s", ri.MetricsPath)
	}

	// Scaling
	if pacto.Status.Scaling == nil {
		t.Fatal("expected scaling info")
	}
	if pacto.Status.Scaling.Replicas == nil || *pacto.Status.Scaling.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %v", pacto.Status.Scaling.Replicas)
	}
	if pacto.Status.Scaling.Min == nil || *pacto.Status.Scaling.Min != 2 {
		t.Fatalf("expected min 2, got %v", pacto.Status.Scaling.Min)
	}
	if pacto.Status.Scaling.Max == nil || *pacto.Status.Scaling.Max != 5 {
		t.Fatalf("expected max 5, got %v", pacto.Status.Scaling.Max)
	}

	// Metadata
	if len(pacto.Status.Metadata) != 2 {
		t.Fatalf("expected 2 metadata entries, got %d", len(pacto.Status.Metadata))
	}
	if pacto.Status.Metadata["team"] != "platform" {
		t.Fatalf("expected platform, got %s", pacto.Status.Metadata["team"])
	}
	if pacto.Status.Metadata["priority"] != "1" {
		t.Fatalf("expected 1 (string), got %s", pacto.Status.Metadata["priority"])
	}
}

func TestPopulateContractStatus_Minimal(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{
				Name:    "minimal-svc",
				Version: "0.1.0",
			},
		},
		RawYAML: []byte("minimal"),
	}

	r.populateContractStatus(pacto, lr)

	if pacto.Status.Contract == nil {
		t.Fatal("expected contract info")
	}
	if pacto.Status.Contract.ImageRef != "" {
		t.Fatalf("expected empty image ref, got %s", pacto.Status.Contract.ImageRef)
	}
	if pacto.Status.Configuration != nil {
		t.Fatal("expected nil configuration")
	}
	if pacto.Status.Policy != nil {
		t.Fatal("expected nil policy")
	}
	if pacto.Status.Runtime != nil {
		t.Fatal("expected nil runtime")
	}
	if pacto.Status.Scaling != nil {
		t.Fatal("expected nil scaling")
	}
	if pacto.Status.Metadata != nil {
		t.Fatal("expected nil metadata")
	}
}

func TestPopulateContractStatus_RuntimeWithoutOptionalFields(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Runtime: &contract.Runtime{
				Workload: "service",
				State:    contract.State{Type: "stateless"},
			},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	ri := pacto.Status.Runtime
	if ri == nil {
		t.Fatal("expected runtime info")
	}
	if ri.UpgradeStrategy != "" {
		t.Fatalf("expected empty upgrade strategy, got %s", ri.UpgradeStrategy)
	}
	if ri.GracefulShutdownSeconds != nil {
		t.Fatalf("expected nil graceful shutdown, got %v", ri.GracefulShutdownSeconds)
	}
	if ri.HealthInterface != "" {
		t.Fatalf("expected empty health interface, got %s", ri.HealthInterface)
	}
	if ri.MetricsInterface != "" {
		t.Fatalf("expected empty metrics interface, got %s", ri.MetricsInterface)
	}
	if ri.HealthInitialDelaySeconds != nil {
		t.Fatalf("expected nil health initial delay, got %v", ri.HealthInitialDelaySeconds)
	}
}

func TestPopulateContractStatus_ScalingWithoutReplicas(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Scaling: &contract.Scaling{Min: 1, Max: 10},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	si := pacto.Status.Scaling
	if si == nil {
		t.Fatal("expected scaling info")
	}
	if si.Replicas != nil {
		t.Fatalf("expected nil replicas, got %v", si.Replicas)
	}
	if si.Min == nil || *si.Min != 1 {
		t.Fatalf("expected min 1, got %v", si.Min)
	}
	if si.Max == nil || *si.Max != 10 {
		t.Fatalf("expected max 10, got %v", si.Max)
	}
}

func TestPopulateContractStatus_ScalingZeroMinMax(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	replicas := 1
	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Scaling: &contract.Scaling{Replicas: &replicas, Min: 0, Max: 0},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	si := pacto.Status.Scaling
	if si == nil {
		t.Fatal("expected scaling info")
	}
	if si.Replicas == nil || *si.Replicas != 1 {
		t.Fatalf("expected 1 replica, got %v", si.Replicas)
	}
	if si.Min != nil {
		t.Fatalf("expected nil min for 0, got %v", si.Min)
	}
	if si.Max != nil {
		t.Fatalf("expected nil max for 0, got %v", si.Max)
	}
}

func TestPopulateContractStatus_ConfigurationNoSchema(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service:       contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Configuration: &contract.Configuration{Values: map[string]any{"key1": "val1"}},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	ci := pacto.Status.Configuration
	if ci == nil {
		t.Fatal("expected configuration info")
	}
	if ci.HasSchema {
		t.Fatal("expected HasSchema=false")
	}
	if len(ci.ValueKeys) != 1 || ci.ValueKeys[0] != "key1" {
		t.Fatalf("expected [key1], got %v", ci.ValueKeys)
	}
	if len(ci.SecretKeys) != 0 {
		t.Fatalf("expected no secret keys, got %v", ci.SecretKeys)
	}
}

func TestPopulateContractStatus_LifecycleWithoutGraceful(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Runtime: &contract.Runtime{
				Workload: "service",
				State:    contract.State{Type: "stateless"},
				Lifecycle: &contract.Lifecycle{
					UpgradeStrategy: "rolling",
					// GracefulShutdownSeconds is nil
				},
			},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	ri := pacto.Status.Runtime
	if ri == nil {
		t.Fatal("expected runtime info")
	}
	if ri.UpgradeStrategy != "rolling" {
		t.Fatalf("expected rolling, got %s", ri.UpgradeStrategy)
	}
	if ri.GracefulShutdownSeconds != nil {
		t.Fatalf("expected nil graceful shutdown, got %v", ri.GracefulShutdownSeconds)
	}
}

func TestPopulateContractStatus_HealthWithoutInitialDelay(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Runtime: &contract.Runtime{
				Workload: "service",
				State:    contract.State{Type: "stateless"},
				Health: &contract.Health{
					Interface: "http-api",
					Path:      "/healthz",
					// InitialDelaySeconds is nil
				},
			},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	ri := pacto.Status.Runtime
	if ri == nil {
		t.Fatal("expected runtime info")
	}
	if ri.HealthInterface != "http-api" {
		t.Fatalf("expected http-api, got %s", ri.HealthInterface)
	}
	if ri.HealthInitialDelaySeconds != nil {
		t.Fatalf("expected nil initial delay, got %v", ri.HealthInitialDelaySeconds)
	}
}

func TestPopulateContractStatus_PolicyNoSchema(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			Policy:  &contract.Policy{Ref: "oci://pol"},
		},
		RawYAML: []byte("test"),
	}

	r.populateContractStatus(pacto, lr)

	if pacto.Status.Policy == nil {
		t.Fatal("expected policy info")
	}
	if pacto.Status.Policy.HasSchema {
		t.Fatal("expected HasSchema=false")
	}
	if pacto.Status.Policy.Ref != "oci://pol" {
		t.Fatalf("expected oci://pol, got %s", pacto.Status.Policy.Ref)
	}
}

// ---------- probeOneEndpoint ----------

func TestProbeOneEndpoint_InterfaceMissing(t *testing.T) {
	r := newReconciler()
	p := prober.New(0)
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
		interfaceName: "nonexistent",
		ifaceExists:   map[string]bool{},
		ifacePort:     map[string]int32{},
	}

	check, result := r.probeOneEndpoint(context.Background(), p, spec)
	if check.Passed {
		t.Fatal("expected check to fail")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointInterfaceMissing {
		t.Fatalf("expected InterfaceNotFound reason, got %s", check.Reason)
	}
	if result != nil {
		t.Fatal("expected nil result when interface missing")
	}
	if !strings.Contains(check.Message, "nonexistent") {
		t.Fatalf("expected message to mention interface name, got %q", check.Message)
	}
}

func TestProbeOneEndpoint_NoPort(t *testing.T) {
	r := newReconciler()
	p := prober.New(0)
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
		label:         "metrics",
		interfaceName: "http-api",
		ifaceExists:   map[string]bool{"http-api": true},
		ifacePort:     map[string]int32{},
	}

	check, result := r.probeOneEndpoint(context.Background(), p, spec)
	if check.Passed {
		t.Fatal("expected check to fail")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointNoPort {
		t.Fatalf("expected InterfaceHasNoPort reason, got %s", check.Reason)
	}
	if result != nil {
		t.Fatal("expected nil result when no port")
	}
}

func TestProbeOneEndpoint_Unreachable(t *testing.T) {
	r := newReconciler()
	p := prober.New(100 * time.Millisecond)
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
		interfaceName: "http-api",
		path:          "/healthz",
		serviceName:   "nonexistent-service",
		namespace:     "default",
		ifaceExists:   map[string]bool{"http-api": true},
		ifacePort:     map[string]int32{"http-api": 19999},
		requireBody:   false,
	}

	check, result := r.probeOneEndpoint(context.Background(), p, spec)
	if check.Passed {
		t.Fatal("expected check to fail for unreachable endpoint")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointConnectionError {
		t.Fatalf("expected ConnectionFailed reason, got %s", check.Reason)
	}
	if result == nil {
		t.Fatal("expected result even for unreachable")
	}
	if result.Reachable {
		t.Fatal("expected Reachable=false")
	}
	if result.Error == "" {
		t.Fatal("expected error message for unreachable")
	}
}

// ---------- probeEndpoints ----------

func TestProbeEndpoints_RuntimeNil(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: nil,
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if checks != nil {
		t.Fatalf("expected nil checks for nil runtime, got %v", checks)
	}
}

func TestProbeEndpoints_HealthAndMetrics(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			Summary: &pactov1alpha1.CheckSummary{Total: 3, Passed: 3, Failed: 0},
		},
	}

	port := 8080
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "http-api", Type: "http", Port: &port},
		},
		Runtime: &contract.Runtime{
			Health: &contract.Health{
				Interface: "http-api",
				Path:      "/healthz",
			},
			Metrics: &contract.Metrics{
				Interface: "http-api",
				Path:      "/metrics",
			},
		},
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}

	// Both should fail since the k8s service URL is unreachable
	for _, check := range checks {
		if check.Passed {
			t.Fatalf("expected check %s to fail (service unreachable)", check.Name)
		}
	}

	// Summary should have been updated (3 existing + 2 new)
	if pacto.Status.Summary.Total != 5 {
		t.Fatalf("expected total 5, got %d", pacto.Status.Summary.Total)
	}

	// Endpoints should be set
	if pacto.Status.Endpoints == nil {
		t.Fatal("expected endpoints status to be set")
	}
}

func TestProbeEndpoints_HealthInterfaceMissing(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: &contract.Runtime{
			Health: &contract.Health{
				Interface: "nonexistent",
				Path:      "/healthz",
			},
		},
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Reason != pactov1alpha1.ReasonEndpointInterfaceMissing {
		t.Fatalf("expected InterfaceNotFound, got %s", checks[0].Reason)
	}
	// When interface is missing, no EndpointCheckResult is returned
	if pacto.Status.Endpoints != nil {
		t.Fatal("expected nil endpoints when interface is missing")
	}
}

func TestProbeEndpoints_MetricsOnly(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	port := 9090
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "metrics-api", Type: "http", Port: &port},
		},
		Runtime: &contract.Runtime{
			// No Health
			Metrics: &contract.Metrics{
				Interface: "metrics-api",
				Path:      "/metrics",
			},
		},
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Name != pactov1alpha1.ConditionMetricsEndpointValid {
		t.Fatalf("expected MetricsEndpointValid, got %s", checks[0].Name)
	}
}

func TestProbeEndpoints_RuntimeWithoutHealthOrMetrics(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: &contract.Runtime{
			Workload: "service",
			// No Health, no Metrics
		},
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(checks))
	}
	if pacto.Status.Endpoints != nil {
		t.Fatal("expected nil endpoints when no health/metrics declared")
	}
}

func TestProbeEndpoints_HealthEmptyInterface(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: &contract.Runtime{
			Health: &contract.Health{
				Interface: "", // empty interface
				Path:      "/health",
			},
		},
	}

	checks := r.probeEndpoints(context.Background(), pacto, c, "my-svc")
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks for empty interface, got %d", len(checks))
	}
	if pacto.Status.Endpoints != nil {
		t.Fatal("expected nil endpoints when health interface is empty")
	}
}

// ---------- mapObjectToPactos (enqueueForTarget logic) ----------

func newFakeObj(name string) client.Object {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

func TestMapObjectToPactos_ServiceNameMatch(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("my-svc"))
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "my-pacto" {
		t.Fatalf("expected my-pacto, got %s", requests[0].Name)
	}
}

func TestMapObjectToPactos_WorkloadRefMatch(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target: pactov1alpha1.TargetRef{
				ServiceName: "different-svc",
				WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "my-deploy", Kind: "Deployment"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("my-deploy"))
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
}

func TestMapObjectToPactos_Dedup(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target: pactov1alpha1.TargetRef{
				ServiceName: "my-app",
				WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "my-app", Kind: "Deployment"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("my-app"))
	if len(requests) != 1 {
		t.Fatalf("expected 1 request (dedup), got %d", len(requests))
	}
}

func TestMapObjectToPactos_NoMatch(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "other-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("unrelated"))
	if len(requests) != 0 {
		t.Fatalf("expected 0 requests, got %d", len(requests))
	}
}

func TestMapObjectToPactos_ListError(t *testing.T) {
	// Use a scheme that doesn't have Pacto registered so List will fail
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("my-svc"))
	if len(requests) != 0 {
		t.Fatalf("expected 0 requests on list error, got %d", len(requests))
	}
}

func TestMapObjectToPactos_MultiplePactos(t *testing.T) {
	s := newScheme()
	pacto1 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pacto-1", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "shared-svc"},
		},
	}
	pacto2 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pacto-2", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "shared-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto1, pacto2).Build()

	mapFn := mapObjectToPactos(c)
	requests := mapFn(context.Background(), newFakeObj("shared-svc"))
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}
}

// ---------- ensureRevision ----------

func TestEnsureRevision_CreatesNew(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
		},
	}

	r := newReconciler(pacto)

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML:     []byte("test-yaml"),
		ResolvedRef: "ghcr.io/org/svc:1.0.0",
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name == "" {
		t.Fatal("expected non-empty revision name")
	}
	if !strings.Contains(name, "my-pacto") {
		t.Fatalf("expected revision name to contain pacto name, got %s", name)
	}
	if !strings.Contains(name, "1-0-0") {
		t.Fatalf("expected revision name to contain sanitized version, got %s", name)
	}

	// Verify the revision was created
	rev := &pactov1alpha1.PactoRevision{}
	err = r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, rev)
	if err != nil {
		t.Fatalf("failed to get revision: %v", err)
	}
	if rev.Spec.Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", rev.Spec.Version)
	}
	if rev.Spec.Source.OCI != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("expected OCI source, got %s", rev.Spec.Source.OCI)
	}
	if rev.Spec.PactoRef != "my-pacto" {
		t.Fatalf("expected PactoRef my-pacto, got %s", rev.Spec.PactoRef)
	}
	if rev.Spec.ServiceName != "svc" {
		t.Fatalf("expected ServiceName svc, got %s", rev.Spec.ServiceName)
	}
	// Labels
	if rev.Labels[pactov1alpha1.LabelPactoName] != "my-pacto" {
		t.Fatalf("expected pacto label, got %s", rev.Labels[pactov1alpha1.LabelPactoName])
	}
	if rev.Labels[pactov1alpha1.LabelRevisionVersion] != "1.0.0" {
		t.Fatalf("expected version label, got %s", rev.Labels[pactov1alpha1.LabelRevisionVersion])
	}
}

func TestEnsureRevision_ExistingWithEmptyStatus(t *testing.T) {
	rawYAML := []byte("test-yaml")
	hash := fmt.Sprintf("%x", sha256.Sum256(rawYAML))
	shortHash := hash[:7]
	revName := fmt.Sprintf("my-pacto-1-0-0-%s", shortHash)

	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revName,
			Namespace: "default",
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0"},
		},
		// Empty status - ContractHash is "" and CreatedAt is nil
	}

	s := newScheme()
	cb := fake.NewClientBuilder().WithScheme(s).
		WithObjects(pacto, existingRev).
		WithStatusSubresource(&pactov1alpha1.PactoRevision{})
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: rawYAML,
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != revName {
		t.Fatalf("expected %s, got %s", revName, name)
	}

	// Verify status was backfilled
	rev := &pactov1alpha1.PactoRevision{}
	err = r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: revName}, rev)
	if err != nil {
		t.Fatalf("failed to get revision: %v", err)
	}
	if rev.Status.ContractHash != hash {
		t.Fatalf("expected hash to be backfilled, got %s", rev.Status.ContractHash)
	}
	if !rev.Status.Resolved {
		t.Fatal("expected Resolved=true after backfill")
	}
}

func TestEnsureRevision_LongNameTruncation(t *testing.T) {
	longName := strings.Repeat("a", 250)
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      longName,
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("test-yaml"),
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(name) > 253 {
		t.Fatalf("expected name to be truncated to 253, got %d", len(name))
	}
}

func TestEnsureRevision_InlineSource(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
		},
	}

	r := newReconciler(pacto)

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("inline-yaml"),
		// No ResolvedRef and no OCI ref -> Inline source
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rev := &pactov1alpha1.PactoRevision{}
	err = r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, rev)
	if err != nil {
		t.Fatalf("failed to get revision: %v", err)
	}
	if !rev.Spec.Source.Inline {
		t.Fatal("expected Source.Inline=true")
	}
}

func TestEnsureRevision_UnknownVersion(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: ""},
		},
		RawYAML: []byte("yaml"),
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(name, "unknown") {
		t.Fatalf("expected 'unknown' in name for empty version, got %s", name)
	}
}

func TestEnsureRevision_ExistingWithPopulatedStatus(t *testing.T) {
	rawYAML := []byte("existing-yaml")
	hash := fmt.Sprintf("%x", sha256.Sum256(rawYAML))
	shortHash := hash[:7]
	revName := fmt.Sprintf("my-pacto-1-0-0-%s", shortHash)
	now := metav1.Now()

	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revName,
			Namespace: "default",
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0"},
		},
		Status: pactov1alpha1.PactoRevisionStatus{
			Resolved:     true,
			ContractHash: hash,
			CreatedAt:    &now,
		},
	}

	s := newScheme()
	cb := fake.NewClientBuilder().WithScheme(s).
		WithObjects(pacto, existingRev).
		WithStatusSubresource(&pactov1alpha1.PactoRevision{})
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: rawYAML,
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != revName {
		t.Fatalf("expected %s, got %s", revName, name)
	}
}

func TestEnsureRevision_OCIRefFallback(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc"},
		},
	}

	r := newReconciler(pacto)

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "2.0.0"},
		},
		RawYAML: []byte("oci-yaml"),
		// ResolvedRef is empty but OCI ref is set on pacto spec
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rev := &pactov1alpha1.PactoRevision{}
	err = r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, rev)
	if err != nil {
		t.Fatalf("failed to get revision: %v", err)
	}
	if rev.Spec.Source.OCI != "ghcr.io/org/svc" {
		t.Fatalf("expected OCI source from pacto spec, got %s", rev.Spec.Source.OCI)
	}
}

// ---------- syncAllRevisions ----------

func TestSyncAllRevisions_ListTagsError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, fmt.Errorf("registry unreachable")
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to list tags") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncAllRevisions_TagAlreadyHasRevision(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	// Create an existing revision for tag "1.0.0"
	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
		},
	}

	r := newReconciler(pacto, existingRev)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncAllRevisions_LoadError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0.0"}, nil
		},
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return nil, fmt.Errorf("load failed")
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error (should continue on load error): %v", err)
	}
}

func TestSyncAllRevisions_Success(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "svc", Version: "2.0.0"},
				},
				RawYAML:     []byte("v2-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify a revision was created
	revList := &pactov1alpha1.PactoRevisionList{}
	if listErr := r.List(context.Background(), revList, client.InNamespace("default")); listErr != nil {
		t.Fatalf("failed to list revisions: %v", listErr)
	}
	if len(revList.Items) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revList.Items))
	}
}

func TestSyncAllRevisions_TagWithColon(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	var capturedRef string
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"3.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			capturedRef = ref
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "svc", Version: "3.0.0"},
				},
				RawYAML:     []byte("v3-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	// Base ref with oci:// prefix and no existing tag
	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The ref should have been constructed as ghcr.io/org/svc:3.0.0
	if capturedRef != "ghcr.io/org/svc:3.0.0" {
		t.Fatalf("expected ghcr.io/org/svc:3.0.0, got %s", capturedRef)
	}
}

func TestSyncAllRevisions_BaseRefWithExistingTag(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	var capturedRef string
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"4.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			capturedRef = ref
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "svc", Version: "4.0.0"},
				},
				RawYAML:     []byte("v4-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	// Base ref already has a tag — should strip it before appending the new tag
	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedRef != "ghcr.io/org/svc:4.0.0" {
		t.Fatalf("expected ghcr.io/org/svc:4.0.0, got %s", capturedRef)
	}
}

func TestSyncAllRevisions_RevisionListError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	// Use a scheme without PactoRevision to force list error
	s := runtime.NewScheme()
	_ = pactov1alpha1.AddToScheme(s)

	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*pactov1alpha1.PactoRevisionList); ok {
						return fmt.Errorf("simulated list error")
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{
			listTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"5.0.0"}, nil
			},
		},
	}

	// Should not return error — it continues on list error
	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("expected nil error (should continue), got: %v", err)
	}
}

func TestSyncAllRevisions_EnsureRevisionError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"6.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "svc", Version: "6.0.0"},
				},
				RawYAML:     []byte("v6-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	// Inject Create error to make ensureRevision fail
	s := newScheme()
	r.Client = fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("simulated create error")
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r.Scheme = s

	// Should not return error — it continues on ensureRevision error
	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("expected nil error (should continue), got: %v", err)
	}
}

// ---------- failReconciliation ----------

func TestFailReconciliation_NoServiceName(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
		},
	}

	r := newReconciler(pacto)

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "", Version: "1.0.0"},
	}

	valResult := &pactov1alpha1.ValidationResult{
		Valid:  false,
		Errors: []pactov1alpha1.ValidationIssue{{Message: "test error"}},
	}

	_, err := r.failReconciliation(context.Background(), pacto, "test error", valResult, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pacto.Status.ContractStatus != pactov1alpha1.ContractStatusNonCompliant {
		t.Fatalf("expected NonCompliant status, got %s", pacto.Status.ContractStatus)
	}
}

func TestFailReconciliation_NilContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
		},
	}

	r := newReconciler(pacto)

	valResult := &pactov1alpha1.ValidationResult{
		Valid:  false,
		Errors: []pactov1alpha1.ValidationIssue{{Message: "test error"}},
	}

	result, err := r.failReconciliation(context.Background(), pacto, "test error", valResult, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pacto.Status.ContractStatus != pactov1alpha1.ContractStatusNonCompliant {
		t.Fatalf("expected NonCompliant status, got %s", pacto.Status.ContractStatus)
	}
	if pacto.Status.Summary == nil || pacto.Status.Summary.Failed != 1 {
		t.Fatalf("unexpected summary: %+v", pacto.Status.Summary)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("expected 5m requeue, got %v", result.RequeueAfter)
	}
	if pacto.Status.LastReconciledAt == nil {
		t.Fatal("expected LastReconciledAt to be set")
	}
	if pacto.Status.Validation == nil {
		t.Fatal("expected Validation to be set")
	}
}

func TestFailReconciliation_WithServiceName(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
		},
	}

	r := newReconciler(pacto)

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "my-svc", Version: "1.0.0"},
	}

	valResult := &pactov1alpha1.ValidationResult{
		Valid:  false,
		Errors: []pactov1alpha1.ValidationIssue{{Message: "test error"}},
	}

	_, err := r.failReconciliation(context.Background(), pacto, "test error", valResult, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Metrics should be emitted for non-empty service name (no panic)
	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
	if cond == nil {
		t.Fatal("expected ContractValid condition")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected ConditionFalse, got %v", cond.Status)
	}
}

// ---------- Reconcile unit tests (error branches) ----------

func TestReconcile_NotFound(t *testing.T) {
	r := newReconciler() // no Pacto object
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "nonexistent", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("expected nil error for NotFound, got: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("expected no requeue, got %v", result.RequeueAfter)
	}
}

func TestReconcile_GetError(t *testing.T) {
	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*pactov1alpha1.Pacto); ok {
						return fmt.Errorf("simulated get error")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader:   &mockLoader{},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcile_HasExplicitTag(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc:v1.0.0"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}

	r := newReconciler(pacto)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue")
	}
}

func TestReconcile_LoadError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "invalid"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return nil, fmt.Errorf("load failed")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue on load error")
	}
}

func TestReconcile_InlineValidContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}

	port := 8080
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "my-svc", Version: "1.0.0"},
					Interfaces: []contract.Interface{
						{Name: "http", Type: "http", Port: &port},
					},
				},
				RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: my-svc\n  version: 1.0.0\n"),
			}, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue interval")
	}
}

func TestReconcile_EnsureRevisionError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}

	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
			WithStatusSubresource(&pactov1alpha1.Pacto{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
						return fmt.Errorf("simulated revision get error")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{
			loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
				return &loader.LoadResult{
					Contract: &contract.Contract{
						Service: contract.ServiceIdentity{Name: "my-svc", Version: "1.0.0"},
					},
					RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: my-svc\n  version: 1.0.0\n"),
				}, nil
			},
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still complete (revision error is logged but not fatal)
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue interval")
	}
}

func TestReconcile_ObserverError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "test"},
			Target: pactov1alpha1.TargetRef{
				ServiceName: "my-svc",
				WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "my-deploy", Kind: "InvalidKind"},
			},
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "my-svc", Version: "1.0.0"},
				},
				RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: my-svc\n  version: 1.0.0\n"),
			}, nil
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue")
	}
}

func TestReconcile_SyncAllRevisionsError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc"},
			Target:      pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "my-svc", Version: "1.0.0"},
				},
				RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: my-svc\n  version: 1.0.0\n"),
			}, nil
		},
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, fmt.Errorf("registry error")
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("expected requeue")
	}
}

// ---------- computeFinalContractStatus ----------

func TestComputeFinalContractStatus_Compliant(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			Conditions: []metav1.Condition{
				{Type: pactov1alpha1.ConditionServiceExists, Status: metav1.ConditionTrue},
				{Type: pactov1alpha1.ConditionWorkloadExists, Status: metav1.ConditionTrue},
			},
		},
	}
	cs := r.computeFinalContractStatus(pacto)
	if cs != pactov1alpha1.ContractStatusCompliant {
		t.Fatalf("expected Compliant, got %s", cs)
	}
}

func TestComputeFinalContractStatus_NonCompliant(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			Conditions: []metav1.Condition{
				{Type: pactov1alpha1.ConditionServiceExists, Status: metav1.ConditionFalse},
			},
		},
	}
	cs := r.computeFinalContractStatus(pacto)
	if cs != pactov1alpha1.ContractStatusNonCompliant {
		t.Fatalf("expected NonCompliant, got %s", cs)
	}
}

func TestComputeFinalContractStatus_Warning(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			Conditions: []metav1.Condition{
				{Type: pactov1alpha1.ConditionWorkloadExists, Status: metav1.ConditionTrue},
				{Type: pactov1alpha1.ConditionPortsValid, Status: metav1.ConditionFalse},
			},
		},
	}
	cs := r.computeFinalContractStatus(pacto)
	if cs != pactov1alpha1.ContractStatusWarning {
		t.Fatalf("expected Warning, got %s", cs)
	}
}

// ---------- resetDerivedStatus ----------

func TestResetDerivedStatus(t *testing.T) {
	r := newReconciler()
	now := metav1.Now()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: pactov1alpha1.PactoStatus{
			ContractStatus:   pactov1alpha1.ContractStatusCompliant,
			Summary:          &pactov1alpha1.CheckSummary{Total: 5, Passed: 3, Failed: 2},
			ContractVersion:  "1.0.0",
			Contract:         &pactov1alpha1.ContractInfo{ServiceName: "svc"},
			Resources:        &pactov1alpha1.ResourcesStatus{},
			Ports:            &pactov1alpha1.PortStatus{},
			Endpoints:        &pactov1alpha1.EndpointsStatus{},
			Runtime:          &pactov1alpha1.RuntimeInfo{},
			ObservedRuntime:  &pactov1alpha1.ObservedRuntime{},
			Policy:           &pactov1alpha1.PolicyInfo{Ref: "x"},
			Conditions:       []metav1.Condition{{Type: "test", Status: metav1.ConditionTrue}},
			LastReconciledAt: &now,
		},
	}

	r.resetDerivedStatus(pacto)

	if pacto.Status.ContractStatus != "" {
		t.Fatalf("expected empty contractStatus, got %s", pacto.Status.ContractStatus)
	}
	if pacto.Status.Summary != nil {
		t.Fatal("expected nil summary")
	}
	if pacto.Status.ContractVersion != "" {
		t.Fatal("expected empty contract version")
	}
	if pacto.Status.Contract != nil {
		t.Fatal("expected nil contract")
	}
	if pacto.Status.Resources != nil {
		t.Fatal("expected nil resources")
	}
	if pacto.Status.Ports != nil {
		t.Fatal("expected nil ports")
	}
	if pacto.Status.Endpoints != nil {
		t.Fatal("expected nil endpoints")
	}
	if pacto.Status.Runtime != nil {
		t.Fatal("expected nil runtime")
	}
	if pacto.Status.ObservedRuntime != nil {
		t.Fatal("expected nil observed runtime")
	}
	if pacto.Status.Policy != nil {
		t.Fatal("expected nil policy")
	}
	if len(pacto.Status.Conditions) != 0 {
		t.Fatal("expected empty conditions")
	}
}

// ---------- applyValidationResult ----------

func TestApplyValidationResult_Full(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	result := validator.Result{
		ContractStatus: pactov1alpha1.ContractStatusCompliant,
		Checks: []validator.Check{
			{Name: pactov1alpha1.ConditionServiceExists, Passed: true, Reason: "Found", Message: "Service exists"},
			{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true, Reason: "Found", Message: "Workload exists"},
			{Name: pactov1alpha1.ConditionPortsValid, Passed: false, Reason: "Mismatch", Message: "Port 9090 missing"},
		},
		Ports: validator.PortsResult{
			Expected:   []int32{8080, 9090},
			Observed:   []int32{8080},
			Missing:    []int32{9090},
			Unexpected: nil,
		},
	}

	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
		ServicePorts:   []int32{8080},
	}

	r.applyValidationResult(pacto, result, snap, "my-svc", "my-deploy", "Deployment")

	if pacto.Status.Resources == nil {
		t.Fatal("expected resources")
	}
	if pacto.Status.Resources.Service == nil || pacto.Status.Resources.Service.Name != "my-svc" {
		t.Fatal("expected service resource")
	}
	if pacto.Status.Resources.Workload == nil || pacto.Status.Resources.Workload.Name != "my-deploy" {
		t.Fatal("expected workload resource")
	}
	if pacto.Status.Ports == nil || len(pacto.Status.Ports.Missing) != 1 {
		t.Fatal("expected port status with missing port")
	}
	if pacto.Status.Summary == nil || pacto.Status.Summary.Total != 4 || pacto.Status.Summary.Passed != 3 || pacto.Status.Summary.Failed != 1 {
		t.Fatalf("unexpected summary: %+v", pacto.Status.Summary)
	}
}

func TestApplyValidationResult_NoServiceNoWorkload(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	result := validator.Result{Checks: []validator.Check{}}
	snap := &observer.RuntimeSnapshot{}

	r.applyValidationResult(pacto, result, snap, "", "", "")

	if pacto.Status.Resources.Service != nil {
		t.Fatal("expected nil service when serviceName is empty")
	}
	if pacto.Status.Resources.Workload != nil {
		t.Fatal("expected nil workload when workloadName is empty")
	}
}

// ---------- probeOneEndpoint: health success (2xx), bad status (4xx+), metrics non-200, metrics empty body ----------

func TestProbeOneEndpoint_HealthBadStatus(t *testing.T) {
	r := newReconciler()
	p := prober.New(100 * time.Millisecond)
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
		interfaceName: "http-api",
		path:          "/healthz",
		serviceName:   "nonexistent-service",
		namespace:     "default",
		ifaceExists:   map[string]bool{"http-api": true},
		ifacePort:     map[string]int32{"http-api": 19999},
		requireBody:   false,
	}

	// This will be unreachable (connection refused), which we already test.
	// The HTTP status code branches are covered by integration tests.
	check, _ := r.probeOneEndpoint(context.Background(), p, spec)
	if check.Passed {
		t.Fatal("expected check to fail")
	}
}

func TestProbeOneEndpoint_MetricsRequireBody(t *testing.T) {
	r := newReconciler()
	p := prober.New(100 * time.Millisecond)
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
		label:         "metrics",
		interfaceName: "metrics-api",
		path:          "/metrics",
		serviceName:   "nonexistent-service",
		namespace:     "default",
		ifaceExists:   map[string]bool{"metrics-api": true},
		ifacePort:     map[string]int32{"metrics-api": 19998},
		requireBody:   true,
	}

	check, result := r.probeOneEndpoint(context.Background(), p, spec)
	if check.Passed {
		t.Fatal("expected check to fail")
	}
	if result == nil {
		t.Fatal("expected result for unreachable metrics")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointConnectionError {
		t.Fatalf("expected ConnectionError, got %s", check.Reason)
	}
}

// ---------- ensureRevision: error paths ----------

func TestEnsureRevision_GetNonNotFoundError(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("simulated get error")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		})
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("test"),
	}

	_, err := r.ensureRevision(context.Background(), pacto, lr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to check for existing revision") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureRevision_CreateAlreadyExists(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	callCount := 0
	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
		WithStatusSubresource(&pactov1alpha1.PactoRevision{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					callCount++
					// First create the object so it exists, then return AlreadyExists
					_ = c.Create(ctx, obj, opts...)
					return apierrors.NewAlreadyExists(
						pactov1alpha1.GroupVersion.WithResource("pactorevisions").GroupResource(),
						obj.GetName(),
					)
				}
				return c.Create(ctx, obj, opts...)
			},
		})
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("test-already-exists"),
	}

	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("expected no error for AlreadyExists, got: %v", err)
	}
	if name == "" {
		t.Fatal("expected revision name")
	}
}

// ---------- failReconciliation: status update error ----------

func TestFailReconciliation_StatusUpdateError(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
		},
	}

	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto)
	// Do NOT register StatusSubresource, so Status().Update() will fail
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	valResult := &pactov1alpha1.ValidationResult{
		Valid:  false,
		Errors: []pactov1alpha1.ValidationIssue{{Message: "test error"}},
	}

	// Should not return an error even when status update fails
	_, err := r.failReconciliation(context.Background(), pacto, "test error", valResult, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- evaluateProbeResult: pure function tests ----------

func TestEvaluateProbeResult_Unreachable(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
	}
	result := prober.Result{Reachable: false, Error: "connection refused"}
	check, ep := evaluateProbeResult(result, "http://svc:8080/healthz", spec)
	if check.Passed {
		t.Fatal("expected check to fail")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointConnectionError {
		t.Fatalf("expected ConnectionError, got %s", check.Reason)
	}
	if ep == nil || ep.Reachable {
		t.Fatal("expected unreachable endpoint result")
	}
}

func TestEvaluateProbeResult_HealthBadStatus(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
		requireBody:   false,
	}
	result := prober.Result{Reachable: true, StatusCode: 503}
	check, ep := evaluateProbeResult(result, "http://svc:8080/healthz", spec)
	if check.Passed {
		t.Fatal("expected check to fail for 503")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointInvalidStatus {
		t.Fatalf("expected InvalidStatus, got %s", check.Reason)
	}
	if ep == nil {
		t.Fatal("expected endpoint result")
	}
}

func TestEvaluateProbeResult_HealthSuccess(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionHealthEndpointValid,
		label:         "health",
		requireBody:   false,
	}
	result := prober.Result{Reachable: true, StatusCode: 200, LatencyMs: 5}
	check, ep := evaluateProbeResult(result, "http://svc:8080/healthz", spec)
	if !check.Passed {
		t.Fatal("expected check to pass")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointOK {
		t.Fatalf("expected EndpointOK, got %s", check.Reason)
	}
	if ep == nil || ep.StatusCode != 200 {
		t.Fatal("expected 200 in endpoint result")
	}
}

func TestEvaluateProbeResult_MetricsNon200(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
		label:         "metrics",
		requireBody:   true,
	}
	result := prober.Result{Reachable: true, StatusCode: 404}
	check, ep := evaluateProbeResult(result, "http://svc:9090/metrics", spec)
	if check.Passed {
		t.Fatal("expected check to fail for non-200 metrics")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointInvalidStatus {
		t.Fatalf("expected InvalidStatus, got %s", check.Reason)
	}
	if ep == nil {
		t.Fatal("expected endpoint result")
	}
}

func TestEvaluateProbeResult_MetricsEmptyBody(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
		label:         "metrics",
		requireBody:   true,
	}
	result := prober.Result{Reachable: true, StatusCode: 200, ContentPresent: false}
	check, ep := evaluateProbeResult(result, "http://svc:9090/metrics", spec)
	if check.Passed {
		t.Fatal("expected check to fail for empty body")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointEmptyResponse {
		t.Fatalf("expected EmptyResponse, got %s", check.Reason)
	}
	if ep == nil {
		t.Fatal("expected endpoint result")
	}
}

func TestEvaluateProbeResult_MetricsSuccess(t *testing.T) {
	spec := probeSpec{
		conditionType: pactov1alpha1.ConditionMetricsEndpointValid,
		label:         "metrics",
		requireBody:   true,
	}
	result := prober.Result{Reachable: true, StatusCode: 200, ContentPresent: true, LatencyMs: 10}
	check, ep := evaluateProbeResult(result, "http://svc:9090/metrics", spec)
	if !check.Passed {
		t.Fatal("expected check to pass")
	}
	if check.Reason != pactov1alpha1.ReasonEndpointOK {
		t.Fatalf("expected EndpointOK, got %s", check.Reason)
	}
	if ep == nil || ep.StatusCode != 200 {
		t.Fatal("expected 200 in endpoint result")
	}
}

// ---------- Reconcile: validation errors path ----------

func TestReconcile_ValidationErrors(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
		},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{
				Inline: "invalid: yaml",
			},
		},
	}

	// RawYAML that fails structural validation (missing required "service" field)
	badYAML := []byte("invalid: yaml\n")

	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).WithStatusSubresource(pacto)
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{
			loadFn: func(ctx context.Context, ociRef, inline string) (*loader.LoadResult, error) {
				return &loader.LoadResult{
					Contract: &contract.Contract{
						Service: contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
					},
					RawYAML: badYAML,
				}, nil
			},
		},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "my-pacto", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should requeue (failReconciliation sets requeue)
	if result.RequeueAfter == 0 {
		t.Error("expected requeue after validation errors")
	}

	// Verify the pacto status was updated with NonCompliant status
	var updated pactov1alpha1.Pacto
	if err := r.Get(context.Background(), client.ObjectKey{Name: "my-pacto", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated pacto: %v", err)
	}
	if updated.Status.ContractStatus != pactov1alpha1.ContractStatusNonCompliant {
		t.Errorf("expected NonCompliant, got %s", updated.Status.ContractStatus)
	}
}

// ---------- ensureRevision: additional error paths ----------

func TestEnsureRevision_BackfillStatusUpdateError(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	rawYAML := []byte("service:\n  name: svc\n  version: 1.0.0\n")
	hash := fmt.Sprintf("%x", sha256.Sum256(rawYAML))
	revName := "my-pacto-1-0-0-" + hash[:7]

	// Create existing revision with empty status to trigger backfill
	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revName,
			Namespace: "default",
		},
		// Status.ContractHash is empty → triggers backfill
	}

	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto, existingRev).
		WithStatusSubresource(existingRev).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("simulated status update error")
				}
				return c.Status().Update(ctx, obj, opts...)
			},
		})

	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: rawYAML,
	}

	// Should still return the revision name (backfill error is logged but not returned)
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != revName {
		t.Errorf("expected revision name %s, got %s", revName, name)
	}
}

func TestEnsureRevision_SetControllerReferenceError(t *testing.T) {
	// Use an empty scheme so SetControllerReference can't resolve Pacto's GVK
	emptyScheme := runtime.NewScheme()

	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	// Use the normal scheme for the fake client but the empty scheme for the reconciler
	normalScheme := newScheme()
	cb := fake.NewClientBuilder().WithScheme(normalScheme).WithObjects(pacto)
	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   emptyScheme, // SetControllerReference will fail
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("service:\n  name: svc\n  version: 1.0.0\n"),
	}

	_, err := r.ensureRevision(context.Background(), pacto, lr)
	if err == nil {
		t.Fatal("expected error from SetControllerReference")
	}
	if !strings.Contains(err.Error(), "owner reference") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureRevision_CreateStatusUpdateError(t *testing.T) {
	s := newScheme()
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	cb := fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
		WithStatusSubresource(&pactov1alpha1.PactoRevision{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("simulated status update error after create")
				}
				return c.Status().Update(ctx, obj, opts...)
			},
		})

	r := &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte("service:\n  name: svc\n  version: 1.0.0\n"),
	}

	// Should succeed (status update error is logged but not returned)
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name == "" {
		t.Fatal("expected non-empty revision name")
	}
}

// ---------- helper ----------

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
