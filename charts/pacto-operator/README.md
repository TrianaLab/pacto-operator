# pacto-operator

Kubernetes operator for Pacto service contract validation

## Installation

```bash
helm install pacto-operator oci://ghcr.io/trianalab/charts/pacto-operator
```

## CRDs

CRDs are installed automatically from the `crds/` directory. Helm installs CRDs before any other resources and does not delete them on `helm uninstall` (by design).

## Dashboard

The operator manages the Pacto dashboard deployment lifecycle. The dashboard image version is automatically determined by the Pacto library version bundled into the controller — it is not user-configurable.

To enable:

```yaml
dashboard:
  enabled: true
```

## Metrics

The controller exposes Prometheus metrics. Enable a ServiceMonitor with:

```yaml
metrics:
  serviceMonitor:
    enabled: true
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for the controller pod |
| dashboard.enabled | bool | `false` | Enable the operator-managed dashboard deployment. The dashboard image is controlled by the operator and derived from the bundled Pacto library version. It is not user-configurable. |
| dashboard.namespace | string | `""` | Namespace for the dashboard (defaults to release namespace) |
| dashboard.ociSecret | string | `""` | Optional Secret name for OCI registry credentials (keys: username, password, token) |
| fullnameOverride | string | `""` | Override the full release name |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/trianalab/pacto-operator/pacto-controller"` | Controller image repository |
| image.tag | string | `""` | Overrides the image tag (default is the chart appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries |
| leaderElection.enabled | bool | `true` | Enable leader election for HA deployments |
| metrics.enabled | bool | `true` | Enable the metrics endpoint |
| metrics.prometheusRule.enabled | bool | `false` | Create PrometheusRule for alerting |
| metrics.secure | bool | `true` | Serve metrics over HTTPS |
| metrics.service.port | int | `8443` | Metrics service port |
| metrics.serviceMonitor.enabled | bool | `false` | Create a Prometheus ServiceMonitor |
| metrics.serviceMonitor.interval | string | `""` | Scrape interval |
| metrics.serviceMonitor.scrapeTimeout | string | `""` | Scrape timeout |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector for the controller pod |
| podAnnotations | object | `{}` | Annotations to add to the controller pod |
| podLabels | object | `{}` | Labels to add to the controller pod |
| podSecurityContext.runAsNonRoot | bool | `true` | Run pod as non-root |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` | Seccomp profile type |
| replicaCount | int | `1` | Number of controller replicas |
| resources.limits.cpu | string | `"500m"` | CPU limit |
| resources.limits.memory | string | `"128Mi"` | Memory limit |
| resources.requests.cpu | string | `"10m"` | CPU request |
| resources.requests.memory | string | `"64Mi"` | Memory request |
| securityContext.allowPrivilegeEscalation | bool | `false` | Disallow privilege escalation |
| securityContext.capabilities.drop | list | `["ALL"]` | Drop all capabilities |
| securityContext.readOnlyRootFilesystem | bool | `true` | Read-only root filesystem |
| securityContext.runAsNonRoot | bool | `true` | Run as non-root |
| securityContext.runAsUser | int | `65532` | Run as UID 65532 (nonroot in distroless) |
| serviceAccount.annotations | object | `{}` | Annotations to add to the ServiceAccount |
| serviceAccount.automount | bool | `true` | Automount API credentials |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for the controller |
| serviceAccount.name | string | `""` | Name override (defaults to fullname) |
| tolerations | list | `[]` | Tolerations for the controller pod |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| edu-diaz |  | <https://edudiaz.dev> |
