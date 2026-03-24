/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

const (
	// LabelPactoName is the label key used on PactoRevision resources to link them to their parent Pacto.
	LabelPactoName = "pacto.trianalab.io/pacto"

	// LabelRevisionVersion is the label key used on PactoRevision resources to store the contract version.
	LabelRevisionVersion = "pacto.trianalab.io/version"
)

// Condition types — each validation produces exactly one condition.
const (
	// ConditionContractValid indicates whether the contract was successfully parsed and validated.
	ConditionContractValid = "ContractValid"

	// ConditionServiceExists indicates whether the target Service exists.
	ConditionServiceExists = "ServiceExists"

	// ConditionWorkloadExists indicates whether the target workload exists.
	ConditionWorkloadExists = "WorkloadExists"

	// ConditionPortsValid indicates whether all declared ports are present on the Service.
	ConditionPortsValid = "PortsValid"

	// ConditionHealthEndpointValid indicates whether the declared health endpoint responds correctly.
	ConditionHealthEndpointValid = "HealthEndpointValid"

	// ConditionMetricsEndpointValid indicates whether the declared metrics endpoint responds correctly.
	ConditionMetricsEndpointValid = "MetricsEndpointValid"

	// ConditionWorkloadTypeMatch indicates whether the runtime workload kind matches the contract declaration.
	ConditionWorkloadTypeMatch = "WorkloadTypeMatch"

	// ConditionStateModelMatch indicates whether the workload's storage matches the contract state model.
	ConditionStateModelMatch = "StateModelMatch"

	// ConditionUpgradeStrategyMatch indicates whether the actual deployment strategy matches the contract.
	ConditionUpgradeStrategyMatch = "UpgradeStrategyMatch"

	// ConditionGracefulShutdownMatch indicates whether terminationGracePeriodSeconds matches the contract.
	ConditionGracefulShutdownMatch = "GracefulShutdownMatch"

	// ConditionImageMatch indicates whether the running container image matches the contract.
	ConditionImageMatch = "ImageMatch"

	// ConditionHealthTimingMatch indicates whether probe initialDelaySeconds matches the contract.
	ConditionHealthTimingMatch = "HealthTimingMatch"
)

// Condition reasons.
const (
	ReasonContractParsed  = "Parsed"
	ReasonContractInvalid = "Invalid"

	ReasonFound    = "Found"
	ReasonNotFound = "NotFound"

	ReasonPortsMatch   = "AllPortsMatch"
	ReasonMissingPorts = "MissingPorts"

	ReasonReferenceOnly = "ReferenceOnly"

	// Endpoint probe reasons.
	ReasonEndpointOK               = "OK"
	ReasonEndpointConnectionError  = "ConnectionFailed"
	ReasonEndpointInvalidStatus    = "InvalidStatusCode"
	ReasonEndpointEmptyResponse    = "EmptyResponse"
	ReasonEndpointNotDeclared      = "NotDeclared"
	ReasonEndpointNoPort           = "InterfaceHasNoPort"
	ReasonEndpointInterfaceMissing = "InterfaceNotFound"

	// Runtime reconciliation reasons.
	ReasonMatch    = "Match"
	ReasonMismatch = "Mismatch"
	ReasonMissing  = "Missing"
	ReasonSkipped  = "Skipped"
)

// Severity levels for runtime reconciliation checks.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Phase values.
const (
	PhaseHealthy   = "Healthy"
	PhaseDegraded  = "Degraded"
	PhaseInvalid   = "Invalid"
	PhaseReference = "Reference"
	PhaseUnknown   = "Unknown"
)
