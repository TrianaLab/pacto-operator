package validator

import (
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto/pkg/contract"
)

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }

// --- checkWorkloadType tests ---

func TestCheckWorkloadType_ServiceDeployment(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "service"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment"}
	check := checkWorkloadType(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: service → Deployment")
	}
}

func TestCheckWorkloadType_ServiceStatefulSet(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "service"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "StatefulSet"}
	check := checkWorkloadType(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: service → StatefulSet")
	}
}

func TestCheckWorkloadType_JobMatch(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "job"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Job"}
	check := checkWorkloadType(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: job → Job")
	}
}

func TestCheckWorkloadType_ScheduledMatch(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "scheduled"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "CronJob"}
	check := checkWorkloadType(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: scheduled → CronJob")
	}
}

func TestCheckWorkloadType_Mismatch(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "job"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment"}
	check := checkWorkloadType(c, snap)
	if check.Passed {
		t.Errorf("expected fail: job → Deployment")
	}
	if check.Severity != pactov1alpha1.SeverityError {
		t.Errorf("expected error severity, got %s", check.Severity)
	}
}

func TestCheckWorkloadType_ServiceMismatchJob(t *testing.T) {
	c := &contract.Contract{Runtime: &contract.Runtime{Workload: "service"}}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Job"}
	check := checkWorkloadType(c, snap)
	if check.Passed {
		t.Errorf("expected fail: service → Job")
	}
}

// --- checkStateModel tests ---

func TestCheckStateModel_StatelessOK(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment"}
	check := checkStateModel(c, snap)
	if !check.Passed {
		t.Errorf("expected pass for stateless with no PVC")
	}
}

func TestCheckStateModel_StatelessWithPVC(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment", HasPVC: true}
	check := checkStateModel(c, snap)
	if check.Passed {
		t.Errorf("expected fail for stateless with PVC")
	}
	if check.Severity != pactov1alpha1.SeverityWarning {
		t.Errorf("expected warning severity, got %s", check.Severity)
	}
}

func TestCheckStateModel_StatefulNotStatefulSet(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateful",
				Persistence: contract.Persistence{Durability: "persistent"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment"}
	check := checkStateModel(c, snap)
	if check.Passed {
		t.Errorf("expected fail: stateful should be StatefulSet")
	}
	if check.Severity != pactov1alpha1.SeverityError {
		t.Errorf("expected error severity, got %s", check.Severity)
	}
}

func TestCheckStateModel_StatefulPersistentNoPVC(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateful",
				Persistence: contract.Persistence{Durability: "persistent"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "StatefulSet"}
	check := checkStateModel(c, snap)
	if check.Passed {
		t.Errorf("expected fail: stateful+persistent needs PVC")
	}
}

func TestCheckStateModel_StatefulPersistentWithPVC(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateful",
				Persistence: contract.Persistence{Durability: "persistent"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "StatefulSet", HasPVC: true}
	check := checkStateModel(c, snap)
	if !check.Passed {
		t.Errorf("expected pass for stateful+persistent+PVC")
	}
}

func TestCheckStateModel_StatefulEphemeral(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "stateful",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "StatefulSet", HasEmptyDir: true}
	check := checkStateModel(c, snap)
	if !check.Passed {
		t.Errorf("expected pass for stateful+ephemeral on StatefulSet")
	}
}

func TestCheckStateModel_HybridPersistentNoPVC(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "hybrid",
				Persistence: contract.Persistence{Durability: "persistent"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment"}
	check := checkStateModel(c, snap)
	if check.Passed {
		t.Errorf("expected fail: hybrid+persistent needs PVC")
	}
	if check.Severity != pactov1alpha1.SeverityError {
		t.Errorf("expected error severity, got %s", check.Severity)
	}
}

func TestCheckStateModel_HybridEphemeral(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			State: contract.State{
				Type:        "hybrid",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{WorkloadKind: "Deployment", HasEmptyDir: true}
	check := checkStateModel(c, snap)
	if !check.Passed {
		t.Errorf("expected pass for hybrid+ephemeral")
	}
}

// --- checkUpgradeStrategy tests ---

func TestCheckUpgradeStrategy_RollingMatch(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{UpgradeStrategy: "rolling"},
		},
	}
	snap := &observer.RuntimeSnapshot{DeploymentStrategy: "RollingUpdate"}
	check := checkUpgradeStrategy(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: rolling → RollingUpdate")
	}
}

func TestCheckUpgradeStrategy_RecreateMatch(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{UpgradeStrategy: "recreate"},
		},
	}
	snap := &observer.RuntimeSnapshot{DeploymentStrategy: "Recreate"}
	check := checkUpgradeStrategy(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: recreate → Recreate")
	}
}

func TestCheckUpgradeStrategy_OrderedMatch(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{UpgradeStrategy: "ordered"},
		},
	}
	snap := &observer.RuntimeSnapshot{PodManagementPolicy: "OrderedReady"}
	check := checkUpgradeStrategy(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: ordered → OrderedReady")
	}
}

func TestCheckUpgradeStrategy_Mismatch(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{UpgradeStrategy: "rolling"},
		},
	}
	snap := &observer.RuntimeSnapshot{DeploymentStrategy: "Recreate"}
	check := checkUpgradeStrategy(c, snap)
	if check.Passed {
		t.Errorf("expected fail: rolling vs Recreate")
	}
	if check.Severity != pactov1alpha1.SeverityWarning {
		t.Errorf("expected warning severity, got %s", check.Severity)
	}
}

func TestCheckUpgradeStrategy_NoStrategy(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{UpgradeStrategy: "rolling"},
		},
	}
	snap := &observer.RuntimeSnapshot{}
	check := checkUpgradeStrategy(c, snap)
	if !check.Passed {
		t.Errorf("expected pass (skipped) when no strategy available")
	}
	if check.Reason != pactov1alpha1.ReasonSkipped {
		t.Errorf("expected Skipped reason, got %s", check.Reason)
	}
}

// --- checkGracefulShutdown tests ---

func TestCheckGracefulShutdown_Match(t *testing.T) {
	gs := 30
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{GracefulShutdownSeconds: &gs},
		},
	}
	snap := &observer.RuntimeSnapshot{TerminationGracePeriod: int64Ptr(30)}
	check := checkGracefulShutdown(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: matching grace period")
	}
}

func TestCheckGracefulShutdown_Mismatch(t *testing.T) {
	gs := 30
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{GracefulShutdownSeconds: &gs},
		},
	}
	snap := &observer.RuntimeSnapshot{TerminationGracePeriod: int64Ptr(60)}
	check := checkGracefulShutdown(c, snap)
	if check.Passed {
		t.Errorf("expected fail: 30 vs 60")
	}
}

func TestCheckGracefulShutdown_Missing(t *testing.T) {
	gs := 30
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Lifecycle: &contract.Lifecycle{GracefulShutdownSeconds: &gs},
		},
	}
	snap := &observer.RuntimeSnapshot{}
	check := checkGracefulShutdown(c, snap)
	if check.Passed {
		t.Errorf("expected fail: no termination grace period")
	}
	if check.Reason != pactov1alpha1.ReasonMissing {
		t.Errorf("expected Missing reason, got %s", check.Reason)
	}
}

// --- checkImage tests ---

func TestCheckImage_ExactMatch(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/service:v1.0.0"},
		},
	}
	snap := &observer.RuntimeSnapshot{ContainerImages: []string{"ghcr.io/org/service:v1.0.0"}}
	check := checkImage(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: exact image match")
	}
}

func TestCheckImage_RepoMatchNoTag(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/service"},
		},
	}
	snap := &observer.RuntimeSnapshot{ContainerImages: []string{"ghcr.io/org/service:v2.0.0"}}
	check := checkImage(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: repo match without tag in contract")
	}
}

func TestCheckImage_Mismatch(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/service:v1.0.0"},
		},
	}
	snap := &observer.RuntimeSnapshot{ContainerImages: []string{"ghcr.io/org/other:v1.0.0"}}
	check := checkImage(c, snap)
	if check.Passed {
		t.Errorf("expected fail: different images")
	}
	if check.Severity != pactov1alpha1.SeverityError {
		t.Errorf("expected error severity, got %s", check.Severity)
	}
}

func TestCheckImage_NoContainers(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/service:v1.0.0"},
		},
	}
	snap := &observer.RuntimeSnapshot{}
	check := checkImage(c, snap)
	if check.Passed {
		t.Errorf("expected fail: no containers")
	}
}

func TestCheckImage_DockerHubNormalization(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "nginx"},
		},
	}
	snap := &observer.RuntimeSnapshot{ContainerImages: []string{"docker.io/library/nginx:latest"}}
	check := checkImage(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: nginx normalizes to docker.io/library/nginx")
	}
}

func TestCheckImage_TagMismatch(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/service:v1.0.0"},
		},
	}
	snap := &observer.RuntimeSnapshot{ContainerImages: []string{"ghcr.io/org/service:v2.0.0"}}
	check := checkImage(c, snap)
	if check.Passed {
		t.Errorf("expected fail: different tags")
	}
}

// --- checkHealthTiming tests ---

func TestCheckHealthTiming_Match(t *testing.T) {
	delay := 10
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Health: &contract.Health{InitialDelaySeconds: &delay},
		},
	}
	snap := &observer.RuntimeSnapshot{HealthProbeInitialDelay: int32Ptr(10)}
	check := checkHealthTiming(c, snap)
	if !check.Passed {
		t.Errorf("expected pass: matching initial delay")
	}
}

func TestCheckHealthTiming_Mismatch(t *testing.T) {
	delay := 10
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Health: &contract.Health{InitialDelaySeconds: &delay},
		},
	}
	snap := &observer.RuntimeSnapshot{HealthProbeInitialDelay: int32Ptr(30)}
	check := checkHealthTiming(c, snap)
	if check.Passed {
		t.Errorf("expected fail: 10 vs 30")
	}
}

func TestCheckHealthTiming_NoProbe(t *testing.T) {
	delay := 10
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Health: &contract.Health{InitialDelaySeconds: &delay},
		},
	}
	snap := &observer.RuntimeSnapshot{}
	check := checkHealthTiming(c, snap)
	if check.Passed {
		t.Errorf("expected fail: no probe configured")
	}
}

// --- imageMatches tests ---

func TestImageMatches(t *testing.T) {
	tests := []struct {
		expected, actual string
		want             bool
	}{
		{"ghcr.io/org/svc:v1", "ghcr.io/org/svc:v1", true},
		{"ghcr.io/org/svc", "ghcr.io/org/svc:v1", true},
		{"ghcr.io/org/svc:v1", "ghcr.io/org/svc:v2", false},
		{"nginx", "docker.io/library/nginx:latest", true},
		{"user/app", "docker.io/user/app:v1", true},
		{"ghcr.io/org/svc@sha256:abc", "ghcr.io/org/svc@sha256:abc", true},
		{"ghcr.io/org/svc@sha256:abc", "ghcr.io/org/svc:v1", false},
	}

	for _, tt := range tests {
		got := imageMatches(tt.expected, tt.actual)
		if got != tt.want {
			t.Errorf("imageMatches(%q, %q) = %v, want %v", tt.expected, tt.actual, got, tt.want)
		}
	}
}

// --- Integration: Validate with runtime checks ---

func TestValidate_WithRuntimeChecks(t *testing.T) {
	gs := 30
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/svc:v1.0.0"},
		},
		Interfaces: []contract.Interface{
			{Name: "http-api", Type: "http", Port: intPtr(8080)},
		},
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "rolling",
				GracefulShutdownSeconds: &gs,
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:          true,
		WorkloadExists:         true,
		WorkloadKind:           "Deployment",
		ServicePorts:           []int32{8080},
		DeploymentStrategy:     "RollingUpdate",
		TerminationGracePeriod: int64Ptr(30),
		ContainerImages:        []string{"ghcr.io/org/svc:v1.0.0"},
	}

	result := Validate(c, snap, true)

	// Should have: ServiceExists, WorkloadExists, PortsValid, WorkloadTypeMatch,
	// StateModelMatch, UpgradeStrategyMatch, GracefulShutdownMatch, ImageMatch
	if len(result.Checks) != 8 {
		t.Errorf("expected 8 checks, got %d", len(result.Checks))
		for _, ch := range result.Checks {
			t.Logf("  %s: passed=%v", ch.Name, ch.Passed)
		}
	}

	for _, ch := range result.Checks {
		if !ch.Passed {
			t.Errorf("expected check %s to pass, got: %s", ch.Name, ch.Message)
		}
	}
}

func TestValidate_RuntimeChecksWarning(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Image: &contract.Image{Ref: "ghcr.io/org/svc:v1.0.0"},
		},
		Runtime: &contract.Runtime{
			Workload: "job",
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists:  true,
		WorkloadKind:    "Deployment",
		ContainerImages: []string{"ghcr.io/org/svc:v1.0.0"},
	}

	result := Validate(c, snap, false)

	// WorkloadType declares "job" but found Deployment — should have a failing check
	hasFailure := false
	for _, ch := range result.Checks {
		if ch.Name == pactov1alpha1.ConditionWorkloadTypeMatch && !ch.Passed {
			hasFailure = true
		}
	}
	if !hasFailure {
		t.Error("expected WorkloadTypeMatch to fail (job vs Deployment)")
	}
}

func TestValidate_NoRuntimeSection(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
		},
	}
	snap := &observer.RuntimeSnapshot{
		ServiceExists:  true,
		WorkloadExists: true,
		WorkloadKind:   "Deployment",
		ServicePorts:   []int32{8080},
	}

	result := Validate(c, snap, true)

	// Without runtime section, only basic checks apply
	if len(result.Checks) != 3 {
		t.Errorf("expected 3 checks (no runtime), got %d", len(result.Checks))
	}
}

func TestValidate_WorkloadNotExistsSkipsRuntimeChecks(t *testing.T) {
	c := &contract.Contract{
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:        "stateless",
				Persistence: contract.Persistence{Durability: "ephemeral"},
			},
		},
	}
	snap := &observer.RuntimeSnapshot{
		WorkloadExists: false,
		WorkloadKind:   "Deployment",
	}

	result := Validate(c, snap, false)

	// Only workload exists check, no runtime checks
	if len(result.Checks) != 1 {
		t.Errorf("expected 1 check when workload doesn't exist, got %d", len(result.Checks))
	}
}
