[![CI](https://github.com/TrianaLab/pacto-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto-operator/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/trianalab/pacto-operator)](https://goreportcard.com/report/github.com/trianalab/pacto-operator)
[![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto-operator)](https://github.com/TrianaLab/pacto-operator/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto Operator

**Kubernetes operator for [Pacto](https://github.com/TrianaLab/pacto) service contract validation at runtime.**

The Pacto Operator watches `Pacto` custom resources in your cluster and continuously validates that running workloads comply with their declared service contracts. It observes deployments, stateful sets, jobs, services, and their runtime properties — then reports compliance status through structured conditions, metrics, and Kubernetes events.

The operator is read-only and non-intrusive: it never modifies your workloads. It only reads cluster state and compares it against the contract.

---

## Architecture

```
Pacto CR ──► Loader ──► Validator ──► Status + Metrics
                            ▲
              Observer ─────┘
          (reads K8s resources)
```

- **Loader** resolves contracts from OCI registries or inline YAML
- **Observer** reads runtime state (workload kind, strategy, images, probes, storage)
- **Validator** is a pure function: contract + snapshot = result (no side effects)
- **Controller** coordinates the pipeline and updates CR status, conditions, and metrics

Six runtime checks run on each reconciliation:

| Check | Severity | What it validates |
|-------|----------|-------------------|
| WorkloadType | error | Deployment vs StatefulSet vs Job matches contract |
| StateModel | error | PVC/emptyDir presence matches contract |
| UpgradeStrategy | warning | RollingUpdate vs Recreate matches contract |
| GracefulShutdown | warning | terminationGracePeriodSeconds alignment |
| Image | warning | Container image matches contract |
| HealthTiming | warning | Probe initialDelaySeconds alignment |

## Installation

### Helm (recommended)

```bash
helm install pacto-operator oci://ghcr.io/trianalab/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace
```

Enable the dashboard:

```bash
helm install pacto-operator oci://ghcr.io/trianalab/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set dashboard.enabled=true
```

See the [chart README](charts/pacto-operator/) for all configuration options.

### Kustomize

```bash
make install   # Install CRDs
make deploy    # Deploy the controller
```

## Quick Start

1. Install the operator (see above).

2. Create a Pacto contract:

   ```yaml
   apiVersion: pacto.trianalab.io/v1alpha1
   kind: Pacto
   metadata:
     name: my-service
   spec:
     contractRef:
       oci: oci://ghcr.io/your-org/contracts/my-service
     target:
       serviceName: my-service
   ```

3. Check status:

   ```bash
   kubectl get pactos
   ```

   The `PHASE` column shows: `Healthy`, `Degraded`, `Invalid`, or `Reference`.

## CRDs

| CRD | Description |
|-----|-------------|
| `Pacto` | Declares a contract and optional target workload for runtime validation |
| `PactoRevision` | Immutable snapshot of a resolved contract version (auto-managed) |

## Dashboard

The operator optionally manages a [Pacto dashboard](https://github.com/TrianaLab/pacto) deployment. When enabled, the operator creates and reconciles the dashboard Deployment, Service, ServiceAccount, and RBAC in the cluster.

The dashboard image version is automatically derived from the Pacto library dependency bundled into the controller. Users enable/disable the dashboard but do not choose the image — it is always version-locked to the controller.

## Metrics

The controller exposes Prometheus metrics via OpenTelemetry:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `pacto_contract_compliance_status` | Gauge | service, namespace | 1 = compliant, 0 = non-compliant |
| `pacto_contract_validation_errors` | Gauge | service, namespace | Count of error-severity failures |
| `pacto_contract_validation_warnings` | Gauge | service, namespace | Count of warning-severity mismatches |
| `pacto_contract_validation_result` | Gauge | service, namespace, check | Per-check result (1=pass, 0=fail) |

Enable a Prometheus ServiceMonitor via Helm:

```yaml
metrics:
  serviceMonitor:
    enabled: true
```

Pre-built alerting rules are available in `config/prometheus/alerts.yaml`.

## Development

### Prerequisites

- Go 1.25+
- Docker
- kubectl
- [Kind](https://kind.sigs.k8s.io/) (for local Kubernetes and e2e tests)
- make

### Build and test

```bash
make build        # Build the controller binary
make test         # Run unit/integration tests (envtest)
make test-e2e     # Run e2e tests on a Kind cluster
make ci           # Run the full CI pipeline locally
make lint         # Run golangci-lint
```

### Local development

Four single-command targets cover all local development modes:

**Local process** (operator runs on your machine, connects to current kube context):

```bash
make run                    # Operator without dashboard
make run-with-dashboard     # Operator with dashboard enabled
```

**Local Kubernetes** (operator runs inside a Kind cluster as a container):

```bash
make deploy-local                  # Build, load into Kind, deploy (no dashboard)
make deploy-local-with-dashboard   # Build, load into Kind, deploy with dashboard
make undeploy-local                # Remove from Kind
```

Local Kubernetes targets automatically build the Docker image, load it into the Kind cluster (`pacto-operator-dev` by default), install CRDs, and deploy the controller. No manual prep steps needed.

The dashboard image is always derived from the Pacto library version in `go.mod` — it is not user-configurable.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development guide.

## Artifacts

| Artifact | Location |
|----------|----------|
| Controller image | `ghcr.io/trianalab/pacto-operator/pacto-controller` |
| Helm chart | `oci://ghcr.io/trianalab/charts/pacto-operator` |

## License

Copyright 2026 TrianaLab.

Licensed under the [MIT License](LICENSE).
