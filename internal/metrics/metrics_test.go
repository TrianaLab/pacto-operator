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
