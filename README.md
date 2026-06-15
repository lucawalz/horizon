# horizon

[![ci](https://github.com/lucawalz/horizon/actions/workflows/ci.yaml/badge.svg)](https://github.com/lucawalz/horizon/actions/workflows/ci.yaml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)

A Cluster-API operator CLI: it adds on-demand capacity to a homelab cluster by scaling node pools and standing up clusters.

## Description

horizon is a thin command-line operator over Cluster API. It gives a small Kubernetes cluster elastic headroom without owning any cloud provisioning itself. When a workload needs more room than the local nodes provide, horizon scales an existing worker pool so new nodes join the cluster, and it can migrate a workload onto those nodes and tear the pool back down afterward. It can also stand up a separate, fully managed cluster on demand.

The substrate horizon operates over lives in the companion [bedrock](https://github.com/lucawalz/bedrock) repository: Cluster API with the Hetzner provider (CAPH) for infrastructure and cluster-api-k3s for bootstrap and control planes, managed by Rancher Turtles, with Tailscale for connectivity and an in-cluster cluster-autoscaler for scale-on-demand. horizon reads and writes Cluster API objects through a kubeconfig context and leaves the definition of infrastructure to bedrock.

### Two modes

horizon drives capacity through one pool engine over MachineDeployments, in two modes selected by the target.

- On-demand nodes (`up`, `down`, `burst`): scale an existing worker MachineDeployment whose machines join the existing home cluster. That cluster's control plane is externally managed, so Cluster API never marks it initialized on its own. A one-time nudge latches that status so workers bootstrap.
- On-demand clusters (`cluster create`, `delete`, `list`): create a separate CAPI-managed cluster with its own KThreesControlPlane, which auto-imports to Rancher. No nudge applies.

### Background

horizon exists so a three-node home cluster can absorb occasional heavy jobs without running extra hardware year-round. bedrock declares the permanent cluster and the CAPI substrate; horizon adds and removes temporary capacity on top of it.

## Architecture

The in-cluster cluster-autoscaler watches for pending pods and scales the autoscaler-managed pools on its own, so routine scale-out needs no laptop. horizon adds explicit control on top: it scales a worker pool up or down, runs a guided burst, and manages on-demand clusters. A burst takes a Velero backup of the target namespace, scales the worker pool up, waits for the new machines to become ready, rewrites workload node affinity onto the pool, and waits for the workload to land on the new nodes.

Nodes are labeled `horizon.dev/pool=<value>` at join time by bedrock's KThreesConfigTemplate. horizon never labels nodes itself; it rewrites workload affinity to target that label. Durable pools and clusters can be rendered into the bedrock git tree for Flux to reconcile. horizon writes the tree but never commits or pushes it.

```mermaid
flowchart LR
  pods[(Pending pods)] --> autoscaler[cluster-autoscaler]
  autoscaler --> md[Worker MachineDeployment]
  horizon[horizon CLI] -->|up / down / burst| md
  horizon -->|cluster create| capi[CAPI-managed cluster]
  md --> caph[CAPH on Hetzner]
  capi --> caph
  caph -. Tailscale + k3s agent .-> cluster[(Home cluster)]
  horizon -->|migrate workload| cluster
```

## Requirements

- Go 1.26 or newer to build.
- A reachable Kubernetes cluster with the CAPI substrate from bedrock installed: CAPH, cluster-api-k3s, and Rancher Turtles.
- A kubeconfig with a context that reaches the management cluster, over Tailscale in the homelab setup.
- Velero in the cluster for backups, and kube-prometheus-stack for the read-only pressure header in `status`.

## Installation

```
go build -o horizon ./cmd/horizon
```

Or install it into the Go bin directory:

```
go install github.com/lucawalz/horizon/cmd/horizon@latest
```

## Usage

Configuration is read from `$HORIZON_CONFIG_DIR/config.yaml`, or `~/.config/horizon/config.yaml` by default. The persistent `--context` flag selects the kubeconfig context, `--cluster` selects the target CAPI cluster, and `--dry-run` prints planned actions without making changes.

Show cluster pressure, pools, machines, the nudge state, and autoscaler activity:

```
$ horizon status
CPU: 0.08/0.80 ●  Mem: 0.16/0.80 ●  Pending pods: 0

NAME       ROLE     CPU%   MEM%   PODS   STATUS   IP
master     master   13%    17%    26     Ready    10.20.0.10
worker-1   worker   6%     8%     16     Ready    10.20.0.11
worker-2   worker   4%     23%    27     Ready    10.20.0.12

POOL           DESIRED   READY   MACHINE   PHASE   NODE   PROVIDER-ID
burst-workers  0         0       -         -       -      -

control-plane: initialized
autoscaler: Health: Healthy
```

Scale the worker pool up to add nodes. When the externally-managed control plane is not yet marked initialized, rerun with `--nudge` to latch it:

```
horizon up --nudge
```

Scale the worker pool back to zero, or remove it entirely:

```
horizon down
horizon down --delete
```

Burst a namespace onto the pool, backing up, scaling, waiting, and migrating the workload:

```
horizon burst --workload <namespace>
```

Create, list, and delete on-demand CAPI-managed clusters:

```
horizon cluster create --name <name>
horizon cluster list
horizon cluster delete --name <name>
```

Render manifests instead of applying live, for GitOps durability:

```
horizon cluster create --name <name> --dry-run   # print to stdout
horizon cluster create --name <name> --write      # write into the bedrock tree
```

Manage Velero backups and restores, and drain a node:

```
horizon backup create --include-namespaces <namespace> --wait
horizon restore create --from-backup <backup>
horizon drain <node>
```

## Configuration

The config file sets the kubeconfig, the bedrock checkout used for GitOps writes, the default pool target, and the display thresholds. A template is in [`config.example.yaml`](config.example.yaml).

Key fields:

- `kubeconfig`: path to the kubeconfig; empty uses the default loading rules.
- `cluster`: default CAPI cluster name; falls back to the pool cluster when unset.
- `bedrock_path`: path to the bedrock git work tree, required only for `--write` GitOps renders. It is resolved to an absolute path and must exist.
- `pools`: the default `namespace` (`caph-system`), `cluster` (`burst`), and `name` (`burst-workers`) for the worker MachineDeployment.
- `thresholds`: the `burst` and `scale_down` scores and the `window` size, retained only for the read-only pressure header in `status`. They no longer drive any scaling decision.

The retired `infra_path` field is rejected at load time; set `bedrock_path` instead.

## How it works

- Routine scale-out is the cluster-autoscaler's job. horizon scales pools it owns directly and deliberately leaves the autoscaler min and max annotations off those pools, so the two scaling paths do not fight.
- A burst rolls back on failure: a failed migration restores the saved affinity and a failed scale returns the pool to its prior replica count.
- The control-plane nudge is a status-subresource write, the one deliberate exception to GitOps durability. It cannot live in git and resets if the Cluster is recreated, so `status` warns when it is unset.
- Workload placement is a contract: bedrock's KThreesConfigTemplate labels nodes `horizon.dev/pool=<value>` at join, and horizon rewrites workload affinity to match.

## Repository layout

```
cmd/horizon/        main entry point
internal/cli/       cobra commands (status, up, down, burst, cluster, backup, restore, drain)
internal/config/    configuration loading and schema
internal/capi/      Cluster API client, pool and cluster operations, manifest rendering, git writes, nudge
internal/k8s/       cluster client, drain, workload migration
internal/prometheus/  pressure queries over a port-forward
internal/velero/    backups and restores
docs/adr/           architecture decision records
```

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the build, test, branch, and commit conventions. In short: `go build ./...`, `go test ./...`, then open a PR against `main`; CI runs the same checks.

## Support

Open an issue on the [GitHub repository](https://github.com/lucawalz/horizon/issues).

## Authors and acknowledgment

Built and maintained by Luca Walz. It builds on cobra, viper, controller-runtime, client-go, the Cluster API libraries, Velero, and the Prometheus client libraries.

## License

Released under the MIT License. See [LICENSE](LICENSE).

## Project status

Actively developed alongside the bedrock homelab.
