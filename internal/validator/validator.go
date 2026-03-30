/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package validator is the single source of truth for runtime validation.
// It is a pure function: contract + snapshot → result.
// No Kubernetes API calls, no side effects.
package validator

import (
	"fmt"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto/pkg/contract"
)

// Check represents a single validation check result.
type Check struct {
	Name     string // condition type: uses constants from pactov1alpha1
	Passed   bool
	Reason   string
	Message  string
	Severity string // "error" or "warning" — empty defaults to error for backward compat
}

// Result is the output of validation.
type Result struct {
	Checks []Check
	Ports  PortsResult
}

// PortsResult is the explicit port comparison.
type PortsResult struct {
	Expected   []int32
	Observed   []int32
	Missing    []int32
	Unexpected []int32
}

// Validate compares a contract against a runtime snapshot.
// This is the single source of truth for all runtime validation.
func Validate(c *contract.Contract, snap *observer.RuntimeSnapshot, hasService bool) Result {
	var result Result

	// Check 1: Service exists (only if a service target was specified)
	if hasService {
		result.Checks = append(result.Checks, checkServiceExists(snap))
	}

	// Check 2: Workload exists
	result.Checks = append(result.Checks, checkWorkloadExists(snap))

	// Check 3: Ports (only if service exists and contract declares ports)
	if hasService && snap.ServiceExists {
		portsCheck, ports := checkPorts(c, snap)
		result.Checks = append(result.Checks, portsCheck)
		result.Ports = ports
	} else if hasService {
		// Service doesn't exist — populate expected ports from contract for status
		result.Ports.Expected = contractPorts(c)
	}

	// Runtime reconciliation checks (only when workload exists)
	if snap.WorkloadExists && c.Runtime != nil {
		result.Checks = append(result.Checks, checkWorkloadType(c, snap))
		result.Checks = append(result.Checks, checkStateModel(c, snap))

		if c.Runtime.Lifecycle != nil {
			if c.Runtime.Lifecycle.UpgradeStrategy != "" {
				result.Checks = append(result.Checks, checkUpgradeStrategy(c, snap))
			}
			if c.Runtime.Lifecycle.GracefulShutdownSeconds != nil {
				result.Checks = append(result.Checks, checkGracefulShutdown(c, snap))
			}
		}

		if c.Service.Image != nil && c.Service.Image.Ref != "" {
			result.Checks = append(result.Checks, checkImage(c, snap))
		}

		if c.Runtime.Health != nil && c.Runtime.Health.InitialDelaySeconds != nil {
			result.Checks = append(result.Checks, checkHealthTiming(c, snap))
		}
	}

	return result
}

func checkServiceExists(snap *observer.RuntimeSnapshot) Check {
	if snap.ServiceExists {
		return Check{
			Name:    pactov1alpha1.ConditionServiceExists,
			Passed:  true,
			Reason:  pactov1alpha1.ReasonFound,
			Message: "Service exists",
		}
	}
	return Check{
		Name:    pactov1alpha1.ConditionServiceExists,
		Passed:  false,
		Reason:  pactov1alpha1.ReasonNotFound,
		Message: "Service not found",
	}
}

func checkWorkloadExists(snap *observer.RuntimeSnapshot) Check {
	if snap.WorkloadExists {
		return Check{
			Name:    pactov1alpha1.ConditionWorkloadExists,
			Passed:  true,
			Reason:  pactov1alpha1.ReasonFound,
			Message: fmt.Sprintf("%s exists", snap.WorkloadKind),
		}
	}
	return Check{
		Name:    pactov1alpha1.ConditionWorkloadExists,
		Passed:  false,
		Reason:  pactov1alpha1.ReasonNotFound,
		Message: fmt.Sprintf("%s not found", snap.WorkloadKind),
	}
}

func checkPorts(c *contract.Contract, snap *observer.RuntimeSnapshot) (Check, PortsResult) {
	expected := contractPorts(c)
	observed := snap.ServicePorts

	observedSet := make(map[int32]bool, len(observed))
	for _, p := range observed {
		observedSet[p] = true
	}

	expectedSet := make(map[int32]bool, len(expected))
	for _, p := range expected {
		expectedSet[p] = true
	}

	var missing, unexpected []int32
	for _, p := range expected {
		if !observedSet[p] {
			missing = append(missing, p)
		}
	}
	for _, p := range observed {
		if !expectedSet[p] {
			unexpected = append(unexpected, p)
		}
	}

	ports := PortsResult{
		Expected:   expected,
		Observed:   observed,
		Missing:    missing,
		Unexpected: unexpected,
	}

	if len(missing) == 0 {
		return Check{
			Name:    pactov1alpha1.ConditionPortsValid,
			Passed:  true,
			Reason:  pactov1alpha1.ReasonPortsMatch,
			Message: "All declared ports are present on the Service",
		}, ports
	}

	return Check{
		Name:    pactov1alpha1.ConditionPortsValid,
		Passed:  false,
		Reason:  pactov1alpha1.ReasonMissingPorts,
		Message: fmt.Sprintf("Missing ports: %v", missing),
	}, ports
}

func contractPorts(c *contract.Contract) []int32 {
	var ports []int32
	for _, iface := range c.Interfaces {
		if iface.Port != nil {
			ports = append(ports, int32(*iface.Port))
		}
	}
	return ports
}

// computeContractStatus derives the contract compliance status from check results.
// NonCompliant: any resource missing (service or workload not found).
// Warning: any check failed (severity error or warning).
// Compliant: all checks pass.
func computeContractStatus(checks []Check) string {
	hasResourceFailure := false
	hasOtherFailure := false

	for _, c := range checks {
		if c.Passed {
			continue
		}
		if c.Name == pactov1alpha1.ConditionServiceExists || c.Name == pactov1alpha1.ConditionWorkloadExists {
			hasResourceFailure = true
		} else {
			hasOtherFailure = true
		}
	}

	if hasResourceFailure {
		return pactov1alpha1.ContractStatusNonCompliant
	}
	if hasOtherFailure {
		return pactov1alpha1.ContractStatusWarning
	}
	return pactov1alpha1.ContractStatusCompliant
}
