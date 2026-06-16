# horizon

[![ci](https://github.com/lucawalz/horizon/actions/workflows/ci.yaml/badge.svg)](https://github.com/lucawalz/horizon/actions/workflows/ci.yaml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)

A Cluster-API operator CLI: it adds on-demand capacity to a homelab cluster by scaling node pools and standing up clusters.

## Description

horizon is a thin command-line operator over Cluster API. It gives a small Kubernetes cluster elastic headroom without owning any cloud provisioning itself. When a workload needs more room than the local nodes provide, horizon scales an existing worker pool so new nodes join the cluster, and it can migrate a workload onto those nodes and tear the pool back down afterward. It can also stand up a separate, fully managed cluster on demand.

The substrate horizon operates over lives in the companion [bedrock](https://github.com/lucawalz/bedrock) repository: Cluster API with the Hetzner provider (CAPH) for infrastructure and cluster-api-k3s for bootstrap and control planes, managed by Rancher Turtles, with Tailscale for connectivity and an in-cluster cluster-autoscaler for scale-on-demand. horizon reads and writes Cluster API objects through a kubeconfig context and leaves the definition of infrastructure to bedrock.

### Pool categories

horizon distinguishes three capacity categories, each with a different owner.

- Elastic pools (`horizon.dev/pool-type=elastic`): autoscaled by the in-cluster cluster-autoscaler, which scales them to zero and back as pending pods demand. horizon can scale an elastic pool by hand, but the autoscaler owns the pool and may override that scale.
- Reserved pools (`horizon.dev/pool-type=reserved`): operator-pinned and kept off the autoscaler's min and max annotations. horizon owns these through its scale, drain-down, and burst actions; this is the default pool type. Reserved pools carry a Flux create-once annotation, so a manual scale sticks.
- Clusters: separate CAPI-managed clusters with their own KThreesControlPlane that auto-import to Rancher. No nudge applies.

The scale and burst actions target a pool type, defaulting to the configured `default_type` (`reserved`). Each type maps to a MachineDeployment name through the `pools.types` config. Pool machines join the existing home cluster, whose control plane is externally managed, so Cluster API never marks it initialized on its own; a one-time nudge latches that status so workers bootstrap.

### Background

horizon exists so a three-node home cluster can absorb occasional heavy jobs without running extra hardware year-round. bedrock declares the permanent cluster and the CAPI substrate; horizon adds and removes temporary capacity on top of it.

## Architecture

The in-cluster cluster-autoscaler watches for pending pods and scales the autoscaler-managed pools on its own, so routine scale-out needs no laptop. horizon adds explicit control on top through its dashboard: it scales a worker pool up or down, runs a guided burst, and manages on-demand clusters. A burst takes a Velero backup of the target namespace, scales the worker pool up, waits for the new machines to become ready, rewrites workload node affinity onto the pool, and waits for the workload to land on the new nodes.

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

horizon is provider-agnostic. It reads and writes Cluster API objects through a kubeconfig and holds no cloud credentials, so the same binary runs over any infrastructure provider that Cluster API supports. The homelab substrate in [bedrock](https://github.com/lucawalz/bedrock) is one concrete instance, not a hard dependency.

### Running horizon on any cluster

The minimum substrate falls into a hard set that horizon always needs and an optional set that gates individual features.

Hard requirements:

- A Kubernetes management cluster and a kubeconfig with a context that reaches it.
- Cluster API core installed, providing the Cluster, MachineDeployment, and Machine CRDs.
- At least one infrastructure provider installed and configured. Cloud credentials and machine templates live in the provider's namespace, managed by Cluster API, never by horizon.
- MachineDeployments labeled `horizon.dev/pool-type=<type>` so horizon recognizes them as pools, with `pools.types` mapping each type to its MachineDeployment name and `pools.namespace` pointing at the namespace where those MachineDeployments live.

For managed `cluster create`, additionally:

- A control-plane provider and a bootstrap provider, for example cluster-api-k3s or kubeadm.
- Either a ClusterClass and the `CLUSTER_TOPOLOGY` feature gate enabled for the `--class` path, or a clusterctl flavor template for the `--flavor` path.

Optional, each gating one feature:

- metrics-server for the dashboard CPU and memory header.
- cluster-autoscaler for the autoscaler status line and elastic-pool scaling.
- Velero for backups, restores, schedules, and the burst workflow.

horizon never calls a cloud API and stores no cloud credentials. The infrastructure provider does all cloud work through Cluster API; horizon only manipulates Cluster API objects.

### Minimal configuration

A minimal `config.yaml` names the kubeconfig, the target cluster, and the pool layout. Theme and `cluster_create` defaults are optional.

```yaml
kubeconfig: ""
cluster: burst
theme: auto

pools:
  namespace: caph-system
  default_type: reserved
  types:
    elastic: elastic-workers
    reserved: reserved-workers

cluster_create:
  class: ""
  worker_class: ""
```

Set `pools.namespace` to the namespace where the chosen provider's MachineDeployments live. The full template is in [`config.example.yaml`](config.example.yaml).

## Installation

Homebrew is the recommended path once a release is published:

```
brew install lucawalz/tap/horizon
```

Building from source needs Go 1.26 or newer:

```
go build -o horizon ./cmd/horizon
```

Or install it into the Go bin directory:

```
go install github.com/lucawalz/horizon/cmd/horizon@latest
```

## Usage

Configuration is read from `$HORIZON_CONFIG_DIR/config.yaml`, or `~/.config/horizon/config.yaml` by default.

Running `horizon` with no subcommand launches the interactive command centre, a Bubble Tea dashboard that both observes the cluster and drives every action. Two launch flags scope it: `--context` selects the kubeconfig context, and `--cluster` selects the target CAPI cluster.

```
horizon
horizon --context homelab --cluster burst
```

### The dashboard

The command centre opens on a split view. A banner names the active context and cluster, a pressure header shows cluster CPU and memory against their display thresholds and the count of pending pods, and panels on the left list the nodes, the pools with their type and replica state, and any separate CAPI-managed clusters. A command log fills the right, recording each command and its output. The dashboard refreshes on its own as long as it is open, so the figures track the cluster without a manual reload.

The pool panel shows the type read from each MachineDeployment's `horizon.dev/pool-type` label, alongside its desired and ready replicas and machine state. The pressure header warns when the externally managed control plane is not yet marked initialized, so the nudge is not silently forgotten.

### Actions

The dashboard is driven by a command line. Pressing `:` focuses a prompt at the bottom and the output streams into the command log on the right. The dashboard refreshes when a command changes cluster state. Destructive commands ask for confirmation first, and long operations such as a burst or a cluster create stream their progress.

The available commands are:

- `up [--type elastic|reserved] [--nudge] [<replicas>]` and `down [--type ...] [--delete]` scale a pool up or to zero, or delete it.
- `nudge [--namespace ns] [--cluster name]` latches the externally managed control plane as initialized.
- `burst <namespace> [--type ...] [--replicas n]` backs up a workload, scales the pool, and migrates the workload onto the new nodes.
- `cluster create <name> --class <cc> [--set k=v ...] [--preview|--write|--apply]` renders, writes, or applies a cluster from a ClusterClass topology. The `--flavor <file>` fallback renders the same actions from a clusterctl flavor template instead; `--class` and `--flavor` are mutually exclusive. `--preview` is the default, `--apply` creates the cluster live, and `--write` renders the manifests into the bedrock tree for Flux to reconcile.
- `cluster delete <name>` and `cluster list` manage CAPI-managed clusters.
- `backup create [--include-namespaces ...] [--wait]`, `backup list`, `backup describe <name>`, and `backup delete <name>` drive Velero backups.
- `restore create --from-backup <name> [--wait]`, `restore list`, and `restore describe <name>` drive Velero restores.
- `schedule create <name> --schedule "<cron>" [--include-namespaces ...]`, `schedule list`, `schedule describe <name>`, and `schedule delete <name>` manage recurring backup schedules.
- `bsl create <name> --provider <p> --bucket <b>` registers a backup storage location CR without provisioning the bucket, and `bsl list` inspects them.
- `drain <node>` cordons a node and evicts its pods.
- `theme [light|dark|auto]` sets the theme directly, or opens a live picker with no argument. The choice persists to the config file.

Navigation is keyboard-only; the mouse was removed so native terminal text selection works as usual. Outside the command line the arrow keys and pgup/pgdn scroll the log, `r` refreshes, `?` toggles help, and `q` quits. Type `help` at the prompt for the full list of commands.

### Non-interactive use

`horizon version` remains as the only non-interactive subcommand and prints the build version.

## Configuration

The config file sets the kubeconfig, the bedrock checkout used for GitOps writes, the default pool target, and the display thresholds. A template is in [`config.example.yaml`](config.example.yaml).

Key fields:

- `kubeconfig`: path to the kubeconfig; empty uses the default loading rules.
- `cluster`: default CAPI cluster name; falls back to the pool cluster when unset.
- `bedrock_path`: path to the bedrock git work tree, required only for the GitOps write action. It is resolved to an absolute path and must exist.
- `theme`: dashboard theme, one of `auto`, `light`, or `dark`; the `:theme` picker writes this field. Defaults to `auto`.
- `cluster_create`: optional defaults for `cluster create`, with `class` and `worker_class` used when the corresponding flags are omitted.
- `pools`: the default `namespace` and `cluster` (`burst`), the `default_type` (`reserved`), the Kubernetes `version` used by `cluster create` when `--version` is omitted, and a `types` map from pool type to MachineDeployment name (`elastic` to `elastic-workers`, `reserved` to `reserved-workers`). Set `namespace` to the namespace where the chosen provider's MachineDeployments live; it defaults to `caph-system` for the bedrock setup.
- `thresholds`: the `burst` and `scale_down` scores and the `window` size, retained only for the read-only pressure header in the dashboard. They no longer drive any scaling decision.

The retired `infra_path` field is rejected at load time; set `bedrock_path` instead.

## Releases

Pushing a `v*` tag triggers the GoReleaser workflow, which builds the darwin and linux binaries, publishes a GitHub release, and updates the Homebrew formula in the tap.

The tap requires a one-time operator setup that cannot be automated from this repository:

1. Create a public `lucawalz/homebrew-tap` repository to hold the generated formula.
2. Add a `HOMEBREW_TAP_GITHUB_TOKEN` repository secret to this repository, holding a personal access token with `contents:write` on the tap.

## How it works

- Routine scale-out is the cluster-autoscaler's job. The autoscaler owns elastic pools and scales them to zero on its own. horizon owns reserved pools, scaling them directly, and deliberately leaves the autoscaler min and max annotations off them so the two scaling paths do not fight.
- A burst rolls back on failure: a failed migration restores the saved affinity and a failed scale returns the pool to its prior replica count.
- The control-plane nudge is a status-subresource write, the one deliberate exception to GitOps durability. It cannot live in git and resets if the Cluster is recreated, so the dashboard warns when it is unset.
- Workload placement is a contract: bedrock's KThreesConfigTemplate labels nodes `horizon.dev/pool=<type>` at join, and horizon rewrites workload affinity to match the targeted pool type.

## Repository layout

```
cmd/horizon/        main entry point
internal/tui/       Bubble Tea command centre and panels
internal/core/      presentation-free query surface and action functions
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
