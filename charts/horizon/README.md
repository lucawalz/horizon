# horizon

Installs the horizon controller, which leases on-demand cloud capacity for a Kubernetes cluster and destroys it when the lease expires.

The chart templates a Deployment, a ServiceAccount, a ClusterRole and binding for the cluster-scoped work, a namespaced Role and binding for leader election and Secret reads, a Service carrying the metrics port, an optional ServiceMonitor for it, and the two `horizon.dev` custom resource definitions.

The web interface is not served in the cluster by this release. It is served locally instead, by `horizon dashboard` on the loopback address of the machine that runs it. The chart templates nothing for the in-cluster mode, so no port, Service entry or Ingress advertises an endpoint the deployed binary does not answer. The values keys return alongside that mode and the authentication it requires. The mode is opt-in when it returns, rather than enabled by default.

## Installing

The chart is published as an OCI artifact:

```
helm install horizon oci://ghcr.io/lucawalz/charts/horizon \
  --namespace horizon-system --create-namespace
```

Installing from a checkout of the repository:

```
helm install horizon ./charts/horizon --namespace horizon-system --create-namespace
```

## Custom resource definitions

The `CapacityLease` and `ProviderConfig` definitions live in `crds/` rather than `templates/`. Helm installs them on first install and never removes them on uninstall. That is deliberate: deleting a custom resource definition cascade-deletes every lease derived from it, and a lease that disappears before its controller has finished teardown strands the provider instances it owns, which keep billing.

Upgrades do not update the definitions either, because Helm leaves `crds/` alone after the initial install. A chart upgrade that carries a schema change needs the new definitions applied directly:

```
kubectl apply -f charts/horizon/crds/
```

`make manifests` regenerates the definitions from the Go types and copies them into `crds/`, and CI fails when the two copies diverge.

## Values

### Image

| Key | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/lucawalz/horizon` | Image repository. |
| `image.tag` | `""` | Image tag; falls back to the chart `appVersion`. The tag `latest` is rejected at template time because immutable tags are required by cluster admission policy. |
| `image.digest` | `""` | Image digest such as `sha256:...`. Takes precedence over `image.tag` when set. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Pull secrets for a private registry. |

### Controller

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Number of controller replicas. Values above 1 require `leaderElection.enabled`, and the chart refuses to render otherwise. |
| `leaderElection.enabled` | `true` | Run leader election so only one replica reconciles a lease. Adds the namespaced Lease permissions. |
| `lease.pollInterval` | `30s` | Fallback interval between lease reconciles. The controller watches Nodes and reacts to a registration or a readiness change as it happens, so this bounds only how long a missed event can go unnoticed. |
| `extraArgs` | `[]` | Additional command line arguments appended to the container. |
| `extraEnv` | `[]` | Additional environment variables, in the Kubernetes `env` list form. |
| `terminationGracePeriodSeconds` | `30` | Grace period for the controller to finish an in-flight reconcile. |
| `priorityClassName` | `""` | Priority class for the controller pod. |

### Networking

| Key | Default | Description |
| --- | --- | --- |
| `ports.metrics` | `8080` | Port the controller binds for metrics. |
| `ports.health` | `8081` | Port the controller binds for the liveness and readiness probes. |
| `service.type` | `ClusterIP` | Service type. |
| `service.annotations` | `{}` | Service annotations. |
| `service.metricsPort` | `8080` | Service port mapped to the metrics endpoint. |

The Service port is named `metrics`, and a ServiceMonitor scraping the controller selects it by that name.

### Metrics scraping

| Key | Default | Description |
| --- | --- | --- |
| `serviceMonitor.enabled` | `false` | Template a ServiceMonitor for the metrics Service. Off by default, because the resource only exists on a cluster running the Prometheus operator. |
| `serviceMonitor.interval` | `30s` | Scrape interval. |
| `serviceMonitor.scrapeTimeout` | `10s` | Scrape timeout. Must not exceed the interval. |
| `serviceMonitor.labels` | `{}` | Extra labels on the ServiceMonitor. A Prometheus operator instance usually selects monitors by a label, so set whatever its `serviceMonitorSelector` matches, commonly `release: kube-prometheus-stack`. |
| `serviceMonitor.relabelings` | `[]` | Relabeling rules applied to the scrape target, in the Prometheus operator `relabelings` list form. |

The ServiceMonitor renders only when `serviceMonitor.enabled` is set and the cluster serves `monitoring.coreos.com/v1`. Without the second condition an install on a cluster with no Prometheus operator would fail on an unknown kind, so the resource is skipped instead and the install notes say it was. Rendering the manifest offline therefore needs the API version declared:

```
helm template horizon ./charts/horizon \
  --set serviceMonitor.enabled=true --api-versions monitoring.coreos.com/v1
```

The monitor is created in the release namespace and selects the chart's own Service by `app.kubernetes.io/name` and `app.kubernetes.io/instance`, so a Prometheus whose `serviceMonitorNamespaceSelector` is empty picks it up wherever the release lives.

### Permissions

| Key | Default | Description |
| --- | --- | --- |
| `rbac.create` | `true` | Create the ClusterRole, Role, and their bindings. |
| `serviceAccount.create` | `true` | Create the ServiceAccount. |
| `serviceAccount.name` | `""` | ServiceAccount name; defaults to the release full name. |
| `serviceAccount.annotations` | `{}` | ServiceAccount annotations. |

The ClusterRole grants full access to `capacityleases` and `providerconfigs` including their status and finalizer subresources; get, list, watch, patch, update, and delete on nodes; get, list, and watch on pods; create on pod evictions; create and patch on events in both the core and `events.k8s.io` API groups; and get, list, watch, and patch on Deployments and StatefulSets. The namespaced Role grants read access to Secrets and, when leader election is on, the Lease permissions it needs.

### Scheduling and resources

| Key | Default | Description |
| --- | --- | --- |
| `resources.requests.cpu` | `25m` | CPU request. |
| `resources.requests.memory` | `64Mi` | Memory request. |
| `resources.limits.memory` | `128Mi` | Memory limit. No CPU limit is set, because throttling the reconcile loop delays teardown of billable capacity. |
| `nodeSelector` | `{}` | Node selector for the controller pod. |
| `tolerations` | `[]` | Tolerations for the controller pod. |
| `affinity` | `{}` | Affinity rules for the controller pod. |
| `topologySpreadConstraints` | `[]` | Topology spread constraints for the controller pod. |
| `podAnnotations` | `{}` | Annotations on the controller pod. |
| `podLabels` | `{}` | Extra labels on the controller pod. |

### Security context

| Key | Default | Description |
| --- | --- | --- |
| `podSecurityContext.runAsNonRoot` | `true` | Refuse to start as root. |
| `podSecurityContext.runAsUser` | `65532` | Numeric user, matching the distroless nonroot user baked into the image. |
| `podSecurityContext.runAsGroup` | `65532` | Numeric group. |
| `podSecurityContext.seccompProfile.type` | `RuntimeDefault` | Seccomp profile. |
| `securityContext.runAsNonRoot` | `true` | Container level repeat of the pod setting. |
| `securityContext.runAsUser` | `65532` | Container level numeric user. |
| `securityContext.allowPrivilegeEscalation` | `false` | Block privilege escalation. |
| `securityContext.readOnlyRootFilesystem` | `true` | Read-only root filesystem. An `emptyDir` is mounted at `/tmp`. |
| `securityContext.capabilities.drop` | `[ALL]` | Drop every capability. |

These defaults satisfy the Pod Security `restricted` profile and the Kyverno policies enforced on the bedrock cluster without further configuration.

## Uninstalling

```
helm uninstall horizon --namespace horizon-system
```

The custom resource definitions and any leases still held survive the uninstall. Removing them is a deliberate second step, and it should follow the deletion of every lease, so that the controller tears down the provider instances before it loses the resources that describe them.
