package validator

import (
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto/pkg/contract"
)

func intPtr(v int) *int { return &v }

func TestValidate_HealthyWithService(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "http-api", Type: "http", Port: intPtr(8080)},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
		ServicePorts:   []int32{8080},
	}

	result := Validate(c, snap, true)

	if result.Phase != pactov1alpha1.PhaseHealthy {
		t.Errorf("expected phase Healthy, got %s", result.Phase)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(result.Checks))
	}
	for _, check := range result.Checks {
		if !check.Passed {
			t.Errorf("expected check %s to pass", check.Name)
		}
	}
}

func TestValidate_ServiceNotFound(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "http-api", Type: "http", Port: intPtr(8080)},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  false,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, true)

	if result.Phase != pactov1alpha1.PhaseInvalid {
		t.Errorf("expected phase Invalid, got %s", result.Phase)
	}
	// Should have ServiceExists=false, WorkloadExists=true, no ports check
	if len(result.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(result.Checks))
	}
	// Expected ports should still be populated from contract
	if len(result.Ports.Expected) != 1 || result.Ports.Expected[0] != 8080 {
		t.Errorf("expected ports to contain 8080, got %v", result.Ports.Expected)
	}
}

func TestValidate_WorkloadNotFound(t *testing.T) {
	c := &contract.Contract{}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: false,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, true)

	if result.Phase != pactov1alpha1.PhaseInvalid {
		t.Errorf("expected phase Invalid, got %s", result.Phase)
	}
}

func TestValidate_PortsMismatch(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "http-api", Type: "http", Port: intPtr(8080)},
			{Name: "metrics", Type: "http", Port: intPtr(9090)},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
		ServicePorts:   []int32{8080, 3000},
	}

	result := Validate(c, snap, true)

	if result.Phase != pactov1alpha1.PhaseDegraded {
		t.Errorf("expected phase Degraded, got %s", result.Phase)
	}
	if len(result.Ports.Missing) != 1 || result.Ports.Missing[0] != 9090 {
		t.Errorf("expected missing port 9090, got %v", result.Ports.Missing)
	}
	if len(result.Ports.Unexpected) != 1 || result.Ports.Unexpected[0] != 3000 {
		t.Errorf("expected unexpected port 3000, got %v", result.Ports.Unexpected)
	}
}

func TestValidate_NoServiceTarget(t *testing.T) {
	c := &contract.Contract{}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, false)

	// No service check, only workload check
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check (workload only), got %d", len(result.Checks))
	}
	if result.Checks[0].Name != pactov1alpha1.ConditionWorkloadExists {
		t.Errorf("expected WorkloadExists check, got %s", result.Checks[0].Name)
	}
	if result.Phase != pactov1alpha1.PhaseHealthy {
		t.Errorf("expected phase Healthy, got %s", result.Phase)
	}
}

func TestValidate_InterfaceWithoutPort(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "events", Type: "event"},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
		ServicePorts:   []int32{8080},
	}

	result := Validate(c, snap, true)

	// Event interface has no port, so expected ports should be empty
	if len(result.Ports.Expected) != 0 {
		t.Errorf("expected no expected ports for event interface, got %v", result.Ports.Expected)
	}
}

func TestValidate_BothResourcesMissing(t *testing.T) {
	c := &contract.Contract{}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  false,
		WorkloadExists: false,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, true)

	if result.Phase != pactov1alpha1.PhaseInvalid {
		t.Errorf("expected phase Invalid, got %s", result.Phase)
	}
	// Both ServiceExists and WorkloadExists should fail
	failCount := 0
	for _, check := range result.Checks {
		if !check.Passed {
			failCount++
		}
	}
	if failCount != 2 {
		t.Errorf("expected 2 failed checks, got %d", failCount)
	}
}

func TestComputePhase_ResourceFailureTakesPrecedence(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionServiceExists, Passed: false},
		{Name: pactov1alpha1.ConditionPortsValid, Passed: false},
	}
	phase := computePhase(checks)
	if phase != pactov1alpha1.PhaseInvalid {
		t.Errorf("expected Invalid (resource failure takes precedence), got %s", phase)
	}
}

func TestComputePhase_AllPassed(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true},
		{Name: pactov1alpha1.ConditionPortsValid, Passed: true},
	}
	phase := computePhase(checks)
	if phase != pactov1alpha1.PhaseHealthy {
		t.Errorf("expected Healthy, got %s", phase)
	}
}

func TestComputePhase_Empty(t *testing.T) {
	phase := computePhase(nil)
	if phase != pactov1alpha1.PhaseHealthy {
		t.Errorf("expected Healthy for empty checks, got %s", phase)
	}
}
