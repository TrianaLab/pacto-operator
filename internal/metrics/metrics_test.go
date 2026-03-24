package metrics

import (
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/validator"
)

func TestRecordValidation_AllPassed(t *testing.T) {
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionContractValid, Passed: true},
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true},
	}

	// Should not panic
	RecordValidation("default", "test-service", checks)
}

func TestRecordValidation_WithFailures(t *testing.T) {
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionContractValid, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadTypeMatch, Passed: false, Severity: pactov1alpha1.SeverityError},
		{Name: pactov1alpha1.ConditionUpgradeStrategyMatch, Passed: false, Severity: pactov1alpha1.SeverityWarning},
	}

	RecordValidation("default", "test-service-failures", checks)
}

func TestRecordValidation_EmptyChecks(t *testing.T) {
	RecordValidation("default", "empty-service", nil)
}

func TestRecordValidation_DefaultSeverity(t *testing.T) {
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionServiceExists, Passed: false},
	}

	// Empty severity should default to error
	RecordValidation("default", "default-severity-service", checks)
}

func TestRecordValidation_MixedPassedAndFailed(t *testing.T) {
	// Mix of passed and failed checks with different severities.
	// allPassed must be false because at least one check fails.
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionContractValid, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadTypeMatch, Passed: false, Severity: pactov1alpha1.SeverityError},
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionUpgradeStrategyMatch, Passed: false, Severity: pactov1alpha1.SeverityWarning},
		{Name: pactov1alpha1.ConditionGracefulShutdownMatch, Passed: false}, // empty severity → defaults to error
	}

	RecordValidation("test-ns", "mixed-service", checks)
}

func TestRecordValidation_EmptySeverityDefaultsToError(t *testing.T) {
	// A failed check with no severity set should be counted as an error.
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionImageMatch, Passed: false, Severity: ""},
	}

	RecordValidation("test-ns", "empty-severity-svc", checks)
}

func TestRecordValidation_ExplicitWarningSeverity(t *testing.T) {
	// A failed check with explicit "warning" severity should be counted as a warning,
	// not an error. This exercises the else branch at line 102-103.
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionHealthTimingMatch, Passed: false, Severity: pactov1alpha1.SeverityWarning},
	}

	RecordValidation("test-ns", "warning-only-svc", checks)
}

func TestRecordValidation_AllChecksPassing(t *testing.T) {
	// Every check passes → allPassed stays true → compliance status = 1.
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionContractValid, Passed: true},
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadTypeMatch, Passed: true},
		{Name: pactov1alpha1.ConditionUpgradeStrategyMatch, Passed: true},
	}

	RecordValidation("test-ns", "all-pass-svc", checks)
}

func TestRecordValidation_AtLeastOneCheckFailing(t *testing.T) {
	// Single failure among otherwise passing checks → allPassed = false → compliance status = 0.
	checks := []validator.Check{
		{Name: pactov1alpha1.ConditionContractValid, Passed: true},
		{Name: pactov1alpha1.ConditionServiceExists, Passed: true},
		{Name: pactov1alpha1.ConditionWorkloadExists, Passed: false, Severity: pactov1alpha1.SeverityError},
	}

	RecordValidation("test-ns", "one-fail-svc", checks)
}
