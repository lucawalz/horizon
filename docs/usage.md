# Usage

The README carries the smallest example; this document carries the whole path, from nothing installed to a leased node registering, and then the web interface the same binary serves. Every flag named along the way is in [docs/cli-reference.md](cli-reference.md).

## Quick start

Nothing needs to exist beforehand except a Kubernetes cluster reachable with `kubectl` at 1.29 or newer, a Hetzner Cloud API token, and a boot image in the Hetzner project; a stock `ubuntu-24.04` image is enough, since horizon boots it with cloud-init rather than expecting anything horizon-specific baked in.

**1. Install the chart.** This installs from a checkout, which is ahead of the published chart:

```
helm install horizon ./charts/horizon --namespace horizon-system --create-namespace
```

**2. Create the Hetzner credential Secret**, in the controller's namespace:

```
kubectl create secret generic horizon-hetzner \
  --namespace horizon-system \
  --from-literal=token=<hetzner-api-token>
```

**3. Create a second Hetzner credential Secret for the node.** Hetzner Cloud API tokens are read-write per project rather than scoped to individual permissions, so the narrowing this token gets is organisational: mint a second token rather than reusing the one from step 2, so the node credential can be revoked on its own, without touching the operator's:

```
kubectl create secret generic horizon-hetzner-node \
  --namespace horizon-system \
  --from-literal=token=<hetzner-api-token-node>
```

**4. Create the join-token Secret**, holding the k3s token an agent presents to join the cluster. On the control plane, this is `/var/lib/rancher/k3s/server/node-token`:

```
kubectl create secret generic horizon-join-token \
  --namespace horizon-system \
  --from-literal=token=<k3s-join-token>
```

**5. Render the cloud-init**, naming the k3s release the node installs. It has to match the control plane, which reports its own as `v1.35.6+k3s1` on the server version line of `kubectl version`; a kubelet newer than the apiserver is outside the Kubernetes version skew policy, and an unpinned install would take whatever release is newest on the day the node boots:

```
horizon cloud-init --server https://<control-plane>:6443 \
  --kubernetes-version <k3s-release> > cloud-init.yaml
```

which prints (the two longer `runcmd` blocks are elided with `...` below; nothing else is):

```yaml
#cloud-config
write_files:
  - path: /etc/rancher/k3s/config.yaml
    permissions: '0600'
    owner: root:root
    content: |
      server: https://<control-plane>:6443
      token: ${HORIZON_JOIN_TOKEN}
      node-label:
        - horizon.dev/pool=reserved
  - path: /etc/horizon/token
    permissions: '0600'
    owner: root:root
    content: |
      ${HORIZON_NODE_TOKEN}
runcmd:
  - |
    curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=<k3s-release> sh -s - agent
  - |
    set -eu
    V=${HORIZON_VERSION}
    ...
    install -D -m0755 "$TMP/horizon" /var/lib/horizon/bin/horizon
  - |
    set -eu
    cat > /etc/systemd/system/horizon-watchdog.service <<'UNIT'
    ...
    ExecStart=/var/lib/horizon/bin/horizon watchdog --max-lifetime=${HORIZON_MAX_LIFETIME} --token-file=/etc/horizon/token --state-dir=/run/horizon
    ...
    systemctl enable --now horizon-watchdog.service
```

The two elided `runcmd` blocks download and checksum-verify the horizon binary at `${HORIZON_VERSION}`, then write and enable `horizon-watchdog.service`; the `${HORIZON_...}` placeholders are correct as printed, since they resolve when `ProviderConfig` builds the provider, not when the CLI renders the template.

A control plane that is only reachable over a VPN needs one more flag, `--flavor-config flannel-iface=<interface>`, naming the tunnel interface on the leased server. Without it the agent registers over the tunnel and then builds its pod network on the public interface, where it reaches nothing in the cluster. Two more flags cover images that are not stock: `--install-kubernetes=false` for an image that already ships k3s, and `--transient-watchdog-unit` for an image whose `/etc/systemd/system` is read-only. See [Images and clusters that are not stock](#images-and-clusters-that-are-not-stock) below.

**6. Create the cloud-init Secret from that file:**

```
kubectl create secret generic horizon-cloud-init \
  --namespace horizon-system \
  --from-file=cloud-init=cloud-init.yaml
```

**7. Apply a `ProviderConfig`** naming the image by name (a custom image works identically with `image: {selector: {...}}`):

```yaml
apiVersion: horizon.dev/v1alpha1
kind: ProviderConfig
metadata:
  name: hetzner
spec:
  type: hetzner
  hetzner:
    credentialsSecretRef: {name: horizon-hetzner, key: token}
    cloudInitSecretRef: {name: horizon-cloud-init, key: cloud-init}
    nodeCredentialSecretRef: {name: horizon-hetzner-node, key: token}
    joinTokenSecretRef: {name: horizon-join-token, key: token}
    image:
      name: ubuntu-24.04
  watchdog: {renewInterval: 1m, slack: 2m, maxLifetime: 8h}
```

That resource plus the Secrets it points at is the whole of horizon's configuration. There is no configuration file and the binary reads none. `spec.type` is `hetzner`, the only accepted value; `credentialsSecretRef`, `cloudInitSecretRef` and exactly one of `image` or the deprecated `imageSelector` are required by the schema; and `spec.watchdog` cross-validates `renewInterval`, `slack` and `maxLifetime` against each other. `nodeCredentialSecretRef` and `joinTokenSecretRef` are optional in the schema and required in practice: a lease is refused while the node credential is unset, and the provider build fails, naming the field, while the join token is unset and a rendered document still carries `${HORIZON_JOIN_TOKEN}`. A `ProviderConfig` is cluster-scoped, so each reference carries a name and a key but no namespace, and the controller resolves it in its own namespace, read from `POD_NAMESPACE` and falling back to the service account namespace file projected into the pod.

**8. Apply a `CapacityLease` and watch the node register:**

```yaml
apiVersion: horizon.dev/v1alpha1
kind: CapacityLease
metadata:
  name: batch-run
spec:
  providerRef: hetzner
  region: nbg1
  size: cx22
  replicas: 1
  duration: 30m
  workload:
    namespaces: [batch]
```

```
kubectl apply -f capacitylease.yaml
kubectl get capacityleases -w
```

`spec.workload` is optional and names a target set: `namespaces` lists at least one namespace, and an optional `selector` narrows the move to the workloads inside them that carry the labels it names. Omitting `spec.workload` adds bare capacity and moves nothing.

`spec.workload.mode` defaults to `move`, which repins the matched workloads onto the leased nodes and restores them at expiry. Setting it to `replicate` leaves each matched workload untouched and runs a lease-owned copy of it on the leased nodes instead, with `spec.workload.burstReplicas` pods, deleted again at teardown. The copy carries the original's labels, so an existing Service reaches the burst replicas as well. An autoscaled workload and a StatefulSet are skipped rather than copied and are named in `status.migrationWarnings` with the reason. [ADR 0035](adr/0035-replicate-a-workload-as-a-lease-owned-copy.md) records why a copy rather than a larger original.

A stock `ubuntu-24.04` node registers within about 90 seconds of boot, carrying `horizon.dev/pool=reserved` from its own cloud-init and `horizon.dev/burst=batch-run:NoSchedule` once the controller matches it to the lease.

## Images and clusters that are not stock

The three capability flags above compose, and each defaults to what the generator did before it existed, so a document rendered earlier is unchanged by their presence. An unusual image is not a reason to leave the generator.

`horizon cloud-init --passthrough` is the remaining step past that, and it emits nothing horizon generates: no join configuration, no pool label, and no watchdog files or unit. It writes only the files and commands named on the command line, for an adopter who owns the whole cloud-init and wants horizon out of it, not for an adopter whose image merely differs from a stock one. The flags that feed the generated content, `--flavor`, `--server`, `--kubernetes-version`, `--label`, `--taint`, `--flavor-config`, `--install-kubernetes`, `--install-watchdog-unit`, `--transient-watchdog-unit`, and `--binary-base-url`, are rejected under `--passthrough` rather than silently discarded. The rendered document is still checked for the `horizon.dev/pool=reserved` node label the provider build requires, so a passthrough document has to carry that label itself. Passthrough also drops the watchdog, and with it the teardown guarantee, which is the reason to reach for a capability flag first.

## Web interface

`horizon dashboard` serves the web interface from the machine it runs on:

```
horizon dashboard
```

It reads the cluster with the caller's own kubeconfig credentials, which is the whole of its authentication, and it therefore binds to `127.0.0.1` and nothing else. The address is not a flag: only the port is, and the loopback address is built inside the server rather than accepted from a caller, so no invocation can widen it. Serving the same interface from inside the cluster is a separate mode and a separate command, `horizon serve`, described in [Serving the web interface in a cluster](serving-the-interface.md).

Five routes are served. The lease list carries the printer columns, one row per `CapacityLease`, with the time each lease has left counting down in its own column. The lease behind each row carries the reservation and the timeline, the conditions, the per-instance table, the namespaces and the workload selector it targets, and the workloads that were drained onto it. Where the controller classified a workload that cannot be replaced before it stops, the detail names that workload and what the move costs it. The panel is a warning rather than a refusal, since the migration goes ahead either way, and it is absent from a lease the controller flagged nothing on. Its sizing reads the same whether the lease named an instance type or asked for a minimum core count, memory, architecture, CPU type and selection strategy, so a lease sized by requirements no longer shows an empty field. A lease sized by requirements also carries the selection panel. It names the strategy, the type it chose and its hourly rate, and the runner-up, alongside how many candidates were offered, how many qualified, and the tally of why the rest were rejected. A lease that named its own type states that absence rather than showing an empty panel. The new lease route is the create form. The machines route lists the `ProviderConfig` resources in the cluster, the reason any of them is unready, and the instance types a chosen one offers in a chosen region. The new config route is the form that creates a `ProviderConfig`.

The countdown is the reason the interface exists, so it is the one reading that never waits on the network. Every deadline is rendered as a `<time>` element carrying the exact instant, and the browser advances the reading from that instant once a second. Nothing is refetched to make it tick, and a lease inside its last five minutes changes colour without asking the server anything. Polling is left with the one job the browser cannot do for itself, observing the phase changes the controller writes, and it runs every 20 seconds. A poll that fails leaves the last answer on screen rather than blanking a view that was reading fine.

The machine type catalogue answers with a named state rather than an empty table, and the interface carries wording for each one: nothing chosen yet, a catalogue this process does not hold, a provider config the refresher has not filled, a region that offers nothing, a read the provider refused, and the listing itself. A catalogue this process does not hold is the ordinary answer: instance types are fetched and cached in memory by the horizon controller process, and the interface, which runs as a separate process, keeps no copy of that cache wherever it runs, so it says so.

The interface creates leases, extends running ones, releases them, and creates provider configurations, and does nothing else that writes: no provider config is edited or deleted, and no Secret is created, read or rendered. The provider config form names the Secrets a configuration points at, which are made with `kubectl` in the namespace the controller runs in, and it reads the created configuration back so a wrong name or a missing key arrives as the reason the configuration is unready rather than as a guess made in the browser. The lease create form asks for a requirement first, since that is what the controller resolves against the provider catalogue, and naming a machine type stays available as the escape hatch the custom resource already provides. The workload namespaces are entered one per field, with another added on request, and each is offered as suggestions where the signed-in identity may list namespaces and stays a plain text field where it may not, because that grant is an optional rule rather than part of the documented role, and a refusal is not worth reporting for a field that works without it. An entry left blank is dropped rather than submitted. A minimum memory carries its unit as a visible choice with the resolved byte count beside it, because `4Gi` is 4,294,967,296 bytes and a machine advertising 4 GB has 4,000,000,000, so the binary suffix silently excludes it. Bounds are shown while typing and enforced by CEL on the apiserver, whose refusal is forwarded with its own status code and message rather than restated. Releasing a lease deletes the `CapacityLease`, which is how the controller is asked for a teardown through its finalizer; the confirmation names the two clocks rather than asking whether the operator is sure, and nothing in the browser destroys a machine itself. Extending one edits `spec.duration` and nothing else, because the controller derives the deadline from that duration on every pass. Only a lengthening is accepted: a shorter duration can move the deadline into the past, which spends the teardown budget before teardown starts and deletes the leased nodes without draining them, so releasing the lease is the path that returns capacity early. An extension measured against a duration the lease no longer carries is refused with `409` rather than applied, and a lease that has expired or is already being released is refused as well, since the controller re-derives no deadline for either. The lease detail carries the earliest lifetime backstop its machines latched, so the ceiling offered is one the controller will not clamp.

Writing is authorised by the caller's own kubeconfig, exactly as `kubectl` is, and it is guarded against the one thing loopback introduces. Any page in the same browser can reach `http://127.0.0.1:8973`, so a mutating request is first checked for the address it was sent to. Its `Host` must name the loopback address and the port the listener actually bound, which is read from the listener rather than from the request, because a name whose zone an attacker owns can be pointed at the loopback address and every other signal the browser then sends is genuine. Past that anchor, the request must also carry a custom header the interface sets on its own calls, `Sec-Fetch-Site: same-origin`, and an `Origin` matching the address it was addressed to where the browser sends one. Any failure answers `403`, and no CORS header is served by anything, because the absent `Access-Control-Allow-Origin` is what makes the preflight fail. A process built with no writer, which is what an embedder that supplies only a reader gets, answers all three mutating routes with `501` and serves every read route unchanged. The reasoning is in [ADR 0027](adr/0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md).

The interface is a single-page application, decided in [ADR 0025](adr/0025-replace-server-rendered-interface-with-embedded-spa.md), which supersedes the server-rendered interface of [ADR 0019](adr/0019-replace-terminal-interface-with-web-and-printer-columns.md). `internal/web/site` holds a Vite project in React and TypeScript styled with Tailwind, and `internal/web/api.go` serves the state behind the routes as JSON at `/api/leases`, `/api/leases/{name}`, `/api/machines` and `/api/namespaces`, with `POST /api/leases`, `PATCH /api/leases/{name}` and `DELETE /api/leases/{name}` behind the guard. The built bundle is committed at `internal/web/site/dist` and embedded with `//go:embed`, so `go build` still needs no JavaScript toolchain, and `go build -tags no_ui` produces the same operator with no interface in it. The rebuild changed how the interface is constructed and how much it can show; it did not change what it reads or how it authenticates.
