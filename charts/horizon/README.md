# horizon

Installs the horizon controller, which leases on-demand cloud capacity for a Kubernetes cluster and destroys it when the lease expires.

The chart templates a Deployment, a ServiceAccount, a ClusterRole and binding for the cluster-scoped work, a namespaced Role and binding for leader election and Secret reads, a Service carrying the metrics port, an optional ServiceMonitor for it, and the two `horizon.dev` custom resource definitions.

The web interface is a second workload, and it is off by default. With `ui.enabled` unset the chart templates nothing for it, so no port, Service entry or route advertises an in-cluster endpoint, and the interface is reached instead through `horizon dashboard` on the loopback address of the machine that runs it. Setting `ui.enabled` adds a separate Deployment, ServiceAccount, ClusterRole and binding, Service and NetworkPolicy, all described under Web interface below. It is opt-in rather than on by default because the in-cluster mode is reachable by anything that can route to it, and it needs an identity provider to authenticate callers before that is safe.

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

Every key below is set with `--set` or a values file, and the default column is what `values.yaml` ships.

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

The Deployment uses the `Recreate` strategy rather than a rolling update, and that is not configurable. The controller writes lease status with a full update, so a replica running the older image would strip any status field its own types do not carry. Recreate stops the old pod before the new one starts, so the two never write the same lease.

A release installed before 0.8.0 left the strategy unset, so the API server defaulted a `strategy.rollingUpdate` block that no field manager owned and server-side apply cannot prune. The upgrade is rejected for holding `rollingUpdate` alongside `type: Recreate` until the stale block and the new type are set in one merge patch:

```
kubectl patch deployment horizon --namespace horizon-system --type merge \
  -p '{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}'
```

Upgrading to 0.11.0 changes the controller Deployment selector. Before 0.11.0 it matched on `app.kubernetes.io/name` and `app.kubernetes.io/instance` alone, and the interface pod carries both, so the controller Deployment owned the interface pod as well and a command such as `kubectl logs deployment/horizon` resolved either pod. The controller pod now carries `app.kubernetes.io/component: controller` and the Deployment selects on it, which leaves the two selectors disjoint. Kubernetes treats `spec.selector` as immutable, so the upgrade is rejected until the old Deployment is deleted once:

```
kubectl delete deployment horizon --namespace horizon-system
helm upgrade horizon oci://ghcr.io/lucawalz/charts/horizon --namespace horizon-system
```

The controller is absent for as long as the upgrade takes, and no leased server depends on it in that window. The node-side watchdog reads its deadline from its own Node object rather than from the controller, and fires on a clock of its own, so teardown holds while the controller is down. That independence is what [ADR 0021](../../docs/adr/0021-node-side-dead-mans-switch-on-two-clocks.md) exists to guarantee.

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

The monitor is created in the release namespace and selects the chart's own Service by `app.kubernetes.io/name`, `app.kubernetes.io/instance` and `app.kubernetes.io/component: controller`, so it matches the metrics Service and never the interface Service, and a Prometheus whose `serviceMonitorNamespaceSelector` is empty picks it up wherever the release lives.

### Web interface

The table below is the single reference for the `ui.*` values; no other document repeats it. [docs/serving-the-interface.md](../../docs/serving-the-interface.md) is the end-to-end guide to this mode, covering what an identity provider has to publish and how to grant an impersonated operator rights over horizon's resources. The decision behind it is [ADR 0028](../../docs/adr/0028-serve-the-interface-in-cluster-behind-a-verified-token-and-impersonation.md).

| Key | Default | Description |
| --- | --- | --- |
| `ui.enabled` | `false` | Template the in-cluster web interface. Off by default; the interface is reachable by anything that can route to it, so it is served only once an identity provider is configured to authenticate callers. |
| `ui.replicaCount` | `1` | Number of interface replicas. |
| `ui.bindHost` | `0.0.0.0` | Address the interface binds inside the pod. |
| `ui.port` | `8082` | Port the interface binds, and the port its Service publishes. |
| `ui.authHeader` | `Authorization` | Request header carrying the token minted by the authenticating proxy. |
| `ui.oidc.issuer` | `""` | Issuer URL of the identity provider. Required when `ui.enabled` is set. The key set used to verify a token is discovered from the issuer, so it is not configured separately. |
| `ui.oidc.audience` | `""` | Audience a token has to name to be accepted. Required when `ui.enabled` is set. |
| `ui.usernameClaim` | `preferred_username` | Token claim carrying the username to impersonate. |
| `ui.groupsClaim` | `groups` | Token claim carrying the groups to impersonate. |
| `ui.externalOrigin` | `""` | Origin a browser reaches the interface at, such as `https://horizon.example.com`. Behind a proxy that origin is not the address the interface bound, and the cross-origin guard compares a mutating request against it. |
| `ui.serviceAccount.create` | `true` | Create the interface ServiceAccount. |
| `ui.serviceAccount.name` | `""` | Interface ServiceAccount name; defaults to the release full name with `-interface` appended. |
| `ui.serviceAccount.annotations` | `{}` | Interface ServiceAccount annotations. |
| `ui.rbac.create` | `true` | Create the interface ClusterRole and its binding. |
| `ui.rbac.impersonateUsers` | `[]` | Usernames the interface may impersonate. Empty leaves the rule unrestricted. |
| `ui.rbac.impersonateGroups` | `[]` | Groups the interface may impersonate. Empty leaves the rule unrestricted. |
| `ui.service.type` | `ClusterIP` | Interface Service type. |
| `ui.service.annotations` | `{}` | Interface Service annotations. |
| `ui.networkPolicy.enabled` | `true` | Template a NetworkPolicy restricting which namespaces may reach the interface port. |
| `ui.networkPolicy.ingressNamespaces` | `[traefik]` | Namespaces allowed to reach the interface, matched on `kubernetes.io/metadata.name`. An empty list admits nothing, which is the direction that fails safe. |

`ui.resources`, `ui.podSecurityContext` and `ui.securityContext` mirror the controller keys of the same name and carry the same defaults, so the interface pod satisfies the same admission policies without further configuration.

The Service is named after the release with `-interface` appended, and it publishes `ui.port`. Both are part of wiring that lives outside the chart: an ingress route and a cluster NetworkPolicy select the Service by that name and that port.

The chart refuses to render when `ui.enabled` is set and `ui.oidc.issuer` or `ui.oidc.audience` is empty, and the failure names whichever is missing. Without a verified issuer and audience the interface would trust whatever identity a request claimed, so rendering it is worse than not rendering it.

The NetworkPolicy restricts ingress only. It selects the interface pods by `app.kubernetes.io/component: interface` and admits the listed namespaces on `ui.port`. Egress is left alone deliberately: the addresses the interface needs, the apiserver and the identity provider, differ from cluster to cluster, and a rule built on a guess would fail closed on a path the chart cannot verify.

#### Identity separation

The interface runs under its own ServiceAccount, and that account is the only subject of the only role in this chart that grants `impersonate`. The controller ClusterRole is untouched by `ui.enabled` and never gains it, so neither role widens the other. The chart refuses to render when `ui.serviceAccount.name` resolves to the controller account, because one shared identity would hand the controller impersonation and hand the interface the permission to delete nodes.

Why `ui.rbac.impersonateUsers` and `ui.rbac.impersonateGroups` matter, and why leaving both empty is not a production value, is in [docs/serving-the-interface.md](../../docs/serving-the-interface.md#narrow-the-impersonation-permission).

### Permissions

| Key | Default | Description |
| --- | --- | --- |
| `rbac.create` | `true` | Create the ClusterRole, Role, and their bindings. |
| `serviceAccount.create` | `true` | Create the ServiceAccount. |
| `serviceAccount.name` | `""` | ServiceAccount name; defaults to the release full name. |
| `serviceAccount.annotations` | `{}` | ServiceAccount annotations. |

The ClusterRole grants full access to `capacityleases` and `providerconfigs` including their status and finalizer subresources; get, list, watch, patch, update, and delete on nodes; get, list, and watch on pods; create on pod evictions; create and patch on events in both the core and `events.k8s.io` API groups; and get, list, watch, and patch on Deployments and StatefulSets. The namespaced Role grants read access to Secrets and, when leader election is on, the Lease permissions it needs.

These keys cover the controller only. The web interface carries its own account and its own role, described under Web interface above, and the two never share either.

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
