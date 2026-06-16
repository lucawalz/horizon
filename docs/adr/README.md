# Architecture Decision Records

These records capture horizon's own design decisions, the choices behind the Go operator that drives on-demand capacity over Cluster API. They follow the [MADR](https://adr.github.io/madr/) format. The infrastructure decisions horizon builds on, the CAPI substrate (CAPH and cluster-api-k3s under Rancher Turtles), Tailscale connectivity, the cluster-autoscaler, and how the cluster is backed up, live in the companion [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.

- [0001. Drive bedrock's Terraform module instead of owning cloud IaC](0001-drive-bedrock-terraform.md) (superseded by 0006)
- [0002. Put cloud calls behind a provider interface](0002-pluggable-provider-interface.md) (superseded by 0006)
- [0003. Scale on a pressure window with hysteresis, not a single reading](0003-pressure-window-hysteresis.md) (superseded by 0008)
- [0004. Migrate a workload with a backup, an affinity rewrite, and an eviction](0004-velero-workload-migration.md) (superseded by 0007)
- [0005. Record burst progress as a resumable phase state machine](0005-resumable-phase-state-machine.md) (superseded by 0007)
- [0006. Become a thin Cluster-API operator instead of a provisioning controller](0006-cluster-api-operator-pivot.md) (accepted)
- [0007. Add on-demand capacity through MachineDeployments in two modes](0007-on-demand-pools-via-machinedeployments.md) (accepted)
- [0008. Retire the laptop watch daemon and WireGuard](0008-retire-watch-daemon-and-wireguard.md) (accepted)
- [0009. Make an interactive TUI the primary interface](0009-interactive-tui-as-primary-interface.md) (accepted)
- [0010. Make cluster create provider-agnostic through a ClusterClass topology](0010-provider-agnostic-cluster-create-via-clusterclass.md) (accepted)
