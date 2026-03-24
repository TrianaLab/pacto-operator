/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package validator

import (
	"fmt"
	"strings"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/observer"
	"github.com/trianalab/pacto/pkg/contract"
)

// checkWorkloadType validates that the Kubernetes resource kind matches the contract's runtime.workload.
//
// Mapping:
//   - service → Deployment or StatefulSet
//   - job     → Job
//   - scheduled → CronJob
func checkWorkloadType(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	expected := c.Runtime.Workload
	actual := snap.WorkloadKind

	match := false
	switch expected {
	case "service":
		match = actual == "Deployment" || actual == "StatefulSet"
	case "job":
		match = actual == "Job"
	case "scheduled":
		match = actual == "CronJob"
	}

	if match {
		return Check{
			Name:     pactov1alpha1.ConditionWorkloadTypeMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonMatch,
			Message:  fmt.Sprintf("Workload type %q matches %s", expected, actual),
			Severity: pactov1alpha1.SeverityError,
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionWorkloadTypeMatch,
		Passed:   false,
		Reason:   pactov1alpha1.ReasonMismatch,
		Message:  fmt.Sprintf("Contract declares workload %q but found %s", expected, actual),
		Severity: pactov1alpha1.SeverityError,
	}
}

// checkStateModel validates that the workload's storage configuration matches the contract's state model.
//
// Rules:
//   - stateful + persistent → expects StatefulSet + PVC
//   - stateful + ephemeral  → expects StatefulSet (emptyDir ok)
//   - stateless             → expects Deployment, no PVC
//   - hybrid + persistent   → expects PVC (StatefulSet or Deployment with PVC volume)
//   - hybrid + ephemeral    → expects Deployment (emptyDir ok)
func checkStateModel(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	stateType := c.Runtime.State.Type
	durability := c.Runtime.State.Persistence.Durability

	switch stateType {
	case "stateful":
		if snap.WorkloadKind != "StatefulSet" {
			return Check{
				Name:     pactov1alpha1.ConditionStateModelMatch,
				Passed:   false,
				Reason:   pactov1alpha1.ReasonMismatch,
				Message:  fmt.Sprintf("Contract declares stateful service but workload is %s (expected StatefulSet)", snap.WorkloadKind),
				Severity: pactov1alpha1.SeverityError,
			}
		}
		if durability == "persistent" && !snap.HasPVC {
			return Check{
				Name:     pactov1alpha1.ConditionStateModelMatch,
				Passed:   false,
				Reason:   pactov1alpha1.ReasonMissing,
				Message:  "Contract declares persistent storage but no PVC found on StatefulSet",
				Severity: pactov1alpha1.SeverityError,
			}
		}

	case "stateless":
		if snap.HasPVC {
			return Check{
				Name:     pactov1alpha1.ConditionStateModelMatch,
				Passed:   false,
				Reason:   pactov1alpha1.ReasonMismatch,
				Message:  "Contract declares stateless service but PVC found on workload",
				Severity: pactov1alpha1.SeverityWarning,
			}
		}

	case "hybrid":
		if durability == "persistent" && !snap.HasPVC {
			return Check{
				Name:     pactov1alpha1.ConditionStateModelMatch,
				Passed:   false,
				Reason:   pactov1alpha1.ReasonMissing,
				Message:  "Contract declares hybrid with persistent storage but no PVC found",
				Severity: pactov1alpha1.SeverityError,
			}
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionStateModelMatch,
		Passed:   true,
		Reason:   pactov1alpha1.ReasonMatch,
		Message:  fmt.Sprintf("State model %s/%s matches workload configuration", stateType, durability),
		Severity: pactov1alpha1.SeverityError,
	}
}

// checkUpgradeStrategy validates that the actual deployment strategy matches the contract declaration.
//
// Mapping:
//   - rolling  → Deployment with RollingUpdate
//   - recreate → Deployment with Recreate
//   - ordered  → StatefulSet with OrderedReady
func checkUpgradeStrategy(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	expected := c.Runtime.Lifecycle.UpgradeStrategy

	var actual string
	switch {
	case snap.DeploymentStrategy != "":
		actual = snap.DeploymentStrategy
	case snap.PodManagementPolicy != "":
		actual = snap.PodManagementPolicy
	default:
		return Check{
			Name:     pactov1alpha1.ConditionUpgradeStrategyMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonSkipped,
			Message:  "No deployment strategy to validate (workload type does not support strategies)",
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	match := false
	switch expected {
	case "rolling":
		match = actual == "RollingUpdate"
	case "recreate":
		match = actual == "Recreate"
	case "ordered":
		match = actual == "OrderedReady"
	}

	if match {
		return Check{
			Name:     pactov1alpha1.ConditionUpgradeStrategyMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonMatch,
			Message:  fmt.Sprintf("Upgrade strategy %q matches %s", expected, actual),
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionUpgradeStrategyMatch,
		Passed:   false,
		Reason:   pactov1alpha1.ReasonMismatch,
		Message:  fmt.Sprintf("Contract declares upgrade strategy %q but found %s", expected, actual),
		Severity: pactov1alpha1.SeverityWarning,
	}
}

// checkGracefulShutdown validates that terminationGracePeriodSeconds matches the contract declaration.
func checkGracefulShutdown(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	expected := int64(*c.Runtime.Lifecycle.GracefulShutdownSeconds)

	if snap.TerminationGracePeriod == nil {
		return Check{
			Name:     pactov1alpha1.ConditionGracefulShutdownMatch,
			Passed:   false,
			Reason:   pactov1alpha1.ReasonMissing,
			Message:  fmt.Sprintf("Contract declares gracefulShutdownSeconds=%d but no terminationGracePeriodSeconds set on pod", expected),
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	actual := *snap.TerminationGracePeriod
	if expected == actual {
		return Check{
			Name:     pactov1alpha1.ConditionGracefulShutdownMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonMatch,
			Message:  fmt.Sprintf("Graceful shutdown period matches (%ds)", expected),
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionGracefulShutdownMatch,
		Passed:   false,
		Reason:   pactov1alpha1.ReasonMismatch,
		Message:  fmt.Sprintf("Contract declares gracefulShutdownSeconds=%d but pod has terminationGracePeriodSeconds=%d", expected, actual),
		Severity: pactov1alpha1.SeverityWarning,
	}
}

// checkImage validates that the running container image matches the contract's declared image.
// Compares the contract's service.image.ref against the first container's image.
func checkImage(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	expected := c.Service.Image.Ref

	if len(snap.ContainerImages) == 0 {
		return Check{
			Name:     pactov1alpha1.ConditionImageMatch,
			Passed:   false,
			Reason:   pactov1alpha1.ReasonMissing,
			Message:  "No container images found on workload",
			Severity: pactov1alpha1.SeverityError,
		}
	}

	actual := snap.ContainerImages[0]

	// Normalize: if expected has no tag, compare only the repository part
	if imageMatches(expected, actual) {
		return Check{
			Name:     pactov1alpha1.ConditionImageMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonMatch,
			Message:  fmt.Sprintf("Container image matches contract (%s)", actual),
			Severity: pactov1alpha1.SeverityError,
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionImageMatch,
		Passed:   false,
		Reason:   pactov1alpha1.ReasonMismatch,
		Message:  fmt.Sprintf("Contract declares image %q but container has %q", expected, actual),
		Severity: pactov1alpha1.SeverityError,
	}
}

// checkHealthTiming validates that the probe's initialDelaySeconds matches the contract declaration.
func checkHealthTiming(c *contract.Contract, snap *observer.RuntimeSnapshot) Check {
	expected := int32(*c.Runtime.Health.InitialDelaySeconds)

	if snap.HealthProbeInitialDelay == nil {
		return Check{
			Name:     pactov1alpha1.ConditionHealthTimingMatch,
			Passed:   false,
			Reason:   pactov1alpha1.ReasonMissing,
			Message:  fmt.Sprintf("Contract declares health initialDelaySeconds=%d but no probe configured on container", expected),
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	actual := *snap.HealthProbeInitialDelay
	if expected == actual {
		return Check{
			Name:     pactov1alpha1.ConditionHealthTimingMatch,
			Passed:   true,
			Reason:   pactov1alpha1.ReasonMatch,
			Message:  fmt.Sprintf("Health probe initialDelaySeconds matches (%ds)", expected),
			Severity: pactov1alpha1.SeverityWarning,
		}
	}

	return Check{
		Name:     pactov1alpha1.ConditionHealthTimingMatch,
		Passed:   false,
		Reason:   pactov1alpha1.ReasonMismatch,
		Message:  fmt.Sprintf("Contract declares health initialDelaySeconds=%d but probe has %d", expected, actual),
		Severity: pactov1alpha1.SeverityWarning,
	}
}

// imageMatches compares two container image references.
// It handles the case where one has a tag and the other doesn't,
// and normalizes docker.io references.
func imageMatches(expected, actual string) bool {
	if expected == actual {
		return true
	}

	// Normalize docker.io short names
	expected = normalizeImageRef(expected)
	actual = normalizeImageRef(actual)

	if expected == actual {
		return true
	}

	// If expected has a tag/digest, require exact match after normalization
	if strings.Contains(expected, ":") || strings.Contains(expected, "@") {
		return false
	}

	// Expected has no tag — match if actual's repository part matches
	actualRepo := actual
	if idx := strings.LastIndex(actual, ":"); idx > strings.LastIndex(actual, "/") {
		actualRepo = actual[:idx]
	}
	if idx := strings.Index(actual, "@"); idx > 0 {
		actualRepo = actual[:idx]
	}

	return expected == actualRepo
}

// normalizeImageRef adds docker.io/library/ prefix for short docker hub references.
func normalizeImageRef(ref string) string {
	// Already fully qualified
	if strings.Contains(ref, "/") && strings.Contains(strings.Split(ref, "/")[0], ".") {
		return ref
	}
	// Single name like "nginx" → "docker.io/library/nginx"
	if !strings.Contains(ref, "/") {
		return "docker.io/library/" + ref
	}
	// user/image → "docker.io/user/image"
	return "docker.io/" + ref
}
