# Command line reference

`horizon` with no subcommand prints help, and every command accepts `--help`, which lists its flags with their defaults:

```
horizon controller   Run the in-cluster capacity lease controller
horizon dashboard    Serve the web interface on loopback
horizon serve        Serve the web interface on a routable address behind an OIDC issuer
horizon watchdog     Enforce the node-side teardown deadline from the leased server itself
horizon cloud-init   Render the cloud-init a burst node needs to join a cluster
horizon version      Print the build version
```

## horizon controller

Runs the in-cluster capacity lease controller, which is what the Helm chart runs. It takes four flags.

| Flag | Default | Description |
| --- | --- | --- |
| `--leader-elect` | `true` | Hold a leader election lease so only one replica reconciles. |
| `--metrics-bind-address` | `:8080` | Address the metrics endpoint binds to. |
| `--health-probe-bind-address` | `:8081` | Address the liveness and readiness endpoints bind to. |
| `--lease-poll-interval` | `30s` | Fallback interval between lease reconciles, used for whatever the node watch misses. |

## horizon dashboard

Serves the web interface from the machine it runs on. It takes one flag, and it is a port rather than an address.

| Flag | Default | Description |
| --- | --- | --- |
| `--port` | `8973` | Loopback port the interface listens on. The host is always `127.0.0.1`. |

The address is not a flag: the loopback address is built inside the server rather than accepted from a caller, so no invocation can widen it. See [Usage](usage.md#web-interface) for what the interface serves.

## horizon serve

Serves the same interface on a routable address, for a team rather than for one operator at one terminal. It takes an address rather than a port, and refuses to start unless it can verify who is calling.

| Flag | Default | Description |
| --- | --- | --- |
| `--bind-address` | `0.0.0.0:8082` | Address the interface binds to. |
| `--oidc-issuer` | none, required | Issuer whose tokens are accepted, and whose discovery document names the key set they are verified against. |
| `--oidc-audience` | none, required | Audience a token has to be issued for. |
| `--auth-header` | `Authorization` | Request header the bearer token arrives in. |
| `--username-claim` | `preferred_username` | Claim the impersonated username is read from. |
| `--groups-claim` | `groups` | Claim the impersonated group memberships are read from. |
| `--external-origin` | empty | Origin a browser reaches the interface at. Unset, every create and release is refused, since the cross-origin guard has no anchor behind a proxy. |

The chart sets these from its `ui.*` values rather than from a command line. See [Serving the web interface in a cluster](serving-the-interface.md).

## horizon watchdog

Runs on the leased server rather than in the cluster, started by the cloud-init that boots it. It is the last teardown layer, and it holds two independent clocks: a monotonic backstop and a renewable wall-clock deadline pulled from the cluster.

| Flag | Default | Description |
| --- | --- | --- |
| `--max-lifetime` | none, required | Age at which the server deletes itself, between `5m` and `24h`. |
| `--token-file` | `/etc/horizon/token` | File holding the provider token used to delete this server. |
| `--kubeconfig` | `/var/lib/rancher/k3s/agent/kubelet.kubeconfig` | Kubelet credential used to read the renewed deadline. A missing file leaves the deadline seeded from the instance label standing. |
| `--node-name` | empty | Server and node to act on; empty means the hostname from the metadata service. |
| `--poll-interval` | `15s` | Interval between deadline checks and node annotation reads. |
| `--metadata-url` | `http://169.254.169.254/hetzner/v1/metadata` | Base URL of the instance metadata service. |
| `--state-dir` | `/run/horizon` | Directory holding the sentinel that records a teardown in progress. |

## horizon cloud-init

Renders the join document a burst node needs. [Usage](usage.md#quick-start) shows the rendered output.

`--server` and `--kubernetes-version` are required unless `--passthrough`, the version only while the flavour installs Kubernetes. It names an exact flavour release, `v1.35.6+k3s1` for k3s, read off the server version line of `kubectl version`. Anything else is refused at render time, so a bare `v1.35.6` taken from the client version line one row above it fails before a node ever boots on it.

`--flavor` (`k3s`, the only one implemented), `--label` and `--taint` (both repeatable), `--install-watchdog-unit` (default `true`), `--binary-base-url`, `--write-file` (`path:permissions:content`, repeatable), `--pre-command`/`--post-command` (repeatable), and `--passthrough` cover the rest; `horizon cloud-init --help` lists all of them with their defaults.

Three further flags say what the image can and cannot do for itself:

| Flag | Default | The image or cluster it is for |
| --- | --- | --- |
| `--install-kubernetes` | `true` | An image that already ships Kubernetes. `false` drops the flavour's install command and keeps everything else, so the join configuration, the node token, and the watchdog are still written. Nothing is left to install a version, so `--kubernetes-version` is rejected alongside it, and matching the control plane becomes the image's problem. |
| `--transient-watchdog-unit` | `false` | An image whose `/etc/systemd/system` is read-only, such as a NixOS one, where a unit file cannot be written and `systemctl enable` cannot create its symlink. The unit is written to `/run/systemd/system` and started instead. Since `/run` is a tmpfs, the write happens from `/var/lib/cloud/scripts/per-boot/horizon-watchdog`, which cloud-init runs on every boot rather than once per instance. It cannot be combined with `--install-watchdog-unit=false`, which is rejected at render time. |
| `--flavor-config key=value` | none | A control plane reached over a VPN, and anything else the flavour's own configuration file covers. Repeatable, merged into `/etc/rancher/k3s/config.yaml` in sorted order. The keys the flavour generates itself, `server`, `token`, `node-label`, and `node-taint`, are rejected, so a label or a taint has exactly one source. |

The document names no CPU architecture: the install block reads `uname -m` on the booted server and downloads the matching release archive, so one blob serves both a `cx` and a `cax` server type.

## horizon version

Prints the build version stamped into the binary at build time. The same stamp is substituted into a rendered cloud-init as `${HORIZON_VERSION}`, so a leased node downloads the binary matching the controller that leased it.
