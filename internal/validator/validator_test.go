package validator

import (
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto/pkg/contract"
)

func intPtr(v int) *int { return &v }

func TestValidate_CompliantWithService(t *testing.T) {
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

	// WorkloadExists should fail
	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionWorkloadExists && ch.Passed {
			t.Error("expected WorkloadExists to fail")
		}
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

func TestComputeContractStatus_ResourceFailureTakesPrecedence(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionServiceExists, Passed: false},
		{Name: pactov1alpha1.ConditionPortsValid, Passed: false},
	}
	cs := computeContractStatus(checks)
	if cs != pactov1alpha1.ContractStatusNonCompliant {
		t.Errorf("expected NonCompliant (resource failure takes precedence), got %s", cs)
	}
}

func TestComputeContractStatus_AllPassed(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true},
		{Name: pactov1alpha1.ConditionPortsValid, Passed: true},
	}
	cs := computeContractStatus(checks)
	if cs != pactov1alpha1.ContractStatusCompliant {
		t.Errorf("expected Compliant, got %s", cs)
	}
}

func TestComputeContractStatus_Empty(t *testing.T) {
	cs := computeContractStatus(nil)
	if cs != pactov1alpha1.ContractStatusCompliant {
		t.Errorf("expected Compliant for empty checks, got %s", cs)
	}
}

// ---------------------------------------------------------------------------
// Validate — runtime guard branches not covered in runtime_checks_test.go
// ---------------------------------------------------------------------------

func TestValidate_LifecycleWithOnlyStrategy(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Workload: "service",
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "rolling",
				GracefulShutdownSeconds: nil,
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists:     true,
		WorkloadKind:       "Deployment",
		DeploymentStrategy: "RollingUpdate",
	}

	result := Validate(c, snap, false)

	// workload + workloadType + stateModel + upgradeStrategy = 4 (no graceful shutdown)
	if len(result.Checks) != 4 {
		t.Errorf("expected 4 checks, got %d", len(result.Checks))
	}
	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionGracefulShutdownMatch {
			t.Error("should not have GracefulShutdownMatch check when GracefulShutdownSeconds is nil")
		}
	}
}

func TestValidate_LifecycleWithOnlyGracefulShutdown(t *testing.T) {
	gs := 30
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Workload: "service",
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "",
				GracefulShutdownSeconds: &gs,
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists:         true,
		WorkloadKind:           "Deployment",
		TerminationGracePeriod: int64Ptr(30),
	}

	result := Validate(c, snap, false)

	// workload + workloadType + stateModel + gracefulShutdown = 4 (no upgrade strategy)
	if len(result.Checks) != 4 {
		t.Errorf("expected 4 checks, got %d", len(result.Checks))
	}
	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionUpgradeStrategyMatch {
			t.Error("should not have UpgradeStrategyMatch check when UpgradeStrategy is empty")
		}
	}
}

func TestValidate_ImageEmptyRef_NoCheck(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Image: &contract.Image{Ref: ""}},
		Runtime: &contract.Runtime{
			Workload: "service",
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, false)

	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionImageMatch {
			t.Error("should not have ImageMatch check when Image.Ref is empty")
		}
	}
}

func TestValidate_HealthNilSkipsCheck(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Workload: "service",
			Health:   nil,
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, false)

	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionHealthTimingMatch {
			t.Error("should not have HealthTimingMatch check when Health is nil")
		}
	}
}

func TestValidate_HealthInitialDelayNilSkipsCheck(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Workload: "service",
			Health:   &contract.Health{InitialDelaySeconds: nil},
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, false)

	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionHealthTimingMatch {
			t.Error("should not have HealthTimingMatch check when InitialDelaySeconds is nil")
		}
	}
}

func TestValidate_AllRuntimeChecksWithHealth(t *testing.T) {
	gs := 30
	delay := 5
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "http", Type: "http", Port: intPtr(8080)},
		},
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "myapp:v1"},
		},
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: ""},
			},
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "rolling",
				GracefulShutdownSeconds: &gs,
			},
			Health: &contract.Health{
				InitialDelaySeconds: &delay,
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:           true,
		WorkloadExists:          true,
		WorkloadKind:            "Deployment",
		ServicePorts:            []int32{8080},
		DeploymentStrategy:      "RollingUpdate",
		TerminationGracePeriod:  int64Ptr(30),
		ContainerImages:         []string{"docker.io/library/myapp:v1"},
		HealthProbeInitialDelay: int32Ptr(5),
	}

	result := Validate(c, snap, true)

	// 3 base + 6 runtime = 9
	if len(result.Checks) != 9 {
		t.Errorf("expected 9 checks, got %d", len(result.Checks))
		for _, ch := range result.Checks {
			t.Logf("  %s: passed=%v reason=%s", ch.Name, ch.Passed, ch.Reason)
		}
	}
	for _, ch := range result.Checks {
		if !ch.Passed {
			t.Errorf("expected all checks to pass, but %s failed: %s", ch.Name, ch.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// normalizeImageRef — covers all 3 branches
// ---------------------------------------------------------------------------

func TestNormalizeImageRef_Branches(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"single name", "nginx", "docker.io/library/nginx"},
		{"single name with tag", "nginx:1.25", "docker.io/library/nginx:1.25"},
		{"user/image", "myuser/myapp", "docker.io/myuser/myapp"},
		{"user/image with tag", "myuser/myapp:v1", "docker.io/myuser/myapp:v1"},
		{"already fully qualified ghcr", "ghcr.io/org/app:v1", "ghcr.io/org/app:v1"},
		{"already fully qualified docker.io", "docker.io/library/nginx:latest", "docker.io/library/nginx:latest"},
		{"quay.io fully qualified", "quay.io/org/image:tag", "quay.io/org/image:tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeImageRef(tt.ref)
			if got != tt.want {
				t.Errorf("normalizeImageRef(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// imageMatches — covers digest stripping and tag stripping branches
// ---------------------------------------------------------------------------

func TestImageMatches_DigestAndTagStripping(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		match    bool
	}{
		// Covers the "expected has no tag" + "actual has colon tag" branch (LastIndex stripping)
		{"no tag expected, actual has tag", "myuser/myapp", "docker.io/myuser/myapp:v1", true},
		// Covers the "expected has no tag" + "actual has digest" branch (Index @ stripping)
		{"no tag expected, actual has digest", "myuser/myapp", "docker.io/myuser/myapp@sha256:abc", true},
		// Covers the "expected has @ digest" early return false
		{"expected has digest, actual different", "nginx@sha256:abc", "docker.io/library/nginx:latest", false},
		// Covers repo mismatch after stripping
		{"no tag expected, repo mismatch", "nginx", "docker.io/library/alpine:latest", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageMatches(tt.expected, tt.actual)
			if got != tt.match {
				t.Errorf("imageMatches(%q, %q) = %v, want %v", tt.expected, tt.actual, got, tt.match)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// computeContractStatus — covers Degraded from non-resource check failure
// ---------------------------------------------------------------------------

func TestComputeContractStatus_DegradedNonResource(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadTypeMatch, Passed: false},
	}
	cs := computeContractStatus(checks)
	if cs != pactov1alpha1.ContractStatusWarning {
		t.Errorf("expected Warning, got %s", cs)
	}
}

func TestComputeContractStatus_InvalidOverridesDegraded(t *testing.T) {
	checks := []Check{
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: false},
		{Name: pactov1alpha1.ConditionWorkloadTypeMatch, Passed: false},
	}
	cs := computeContractStatus(checks)
	if cs != pactov1alpha1.ContractStatusNonCompliant {
		t.Errorf("expected NonCompliant (resource failure overrides), got %s", cs)
	}
}
