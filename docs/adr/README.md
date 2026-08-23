# Architecture Decision Records

These records capture horizon's own design decisions, the choices behind the Go operator that leases on-demand capacity with a guaranteed teardown. They follow the [MADR](https://adr.github.io/madr/) format. The infrastructure decisions horizon builds on, the node image pipeline, Tailscale connectivity, and how the cluster is backed up, live in the companion [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.

The arc runs from a Terraform driver, through a Cluster API operator, to a standalone burst scaler, and now to a lease controller. Superseded records are kept rather than deleted, because the reasoning that was later overturned is the useful part.

- [0001. Drive bedrock's Terraform module instead of owning cloud IaC](0001-drive-bedrock-terraform.md) (superseded by 0006)
- [0002. Put cloud calls behind a provider interface](0002-pluggable-provider-interface.md) (superseded by 0006)
- [0003. Scale on a pressure window with hysteresis, not a single reading](0003-pressure-window-hysteresis.md) (superseded by 0008)
- [0004. Migrate a workload with a backup, an affinity rewrite, and an eviction](0004-velero-workload-migration.md) (superseded by 0007)
- [0005. Record burst progress as a resumable phase state machine](0005-resumable-phase-state-machine.md) (superseded by 0007)
- [0006. Become a thin Cluster-API operator instead of a provisioning controller](0006-cluster-api-operator-pivot.md) (accepted)
- [0007. Add on-demand capacity through MachineDeployments in two modes](0007-on-demand-pools-via-machinedeployments.md) (accepted)
- [0008. Retire the laptop watch daemon and WireGuard](0008-retire-watch-daemon-and-wireguard.md) (accepted)
- [0009. Make an interactive TUI the primary interface](0009-interactive-tui-as-primary-interface.md) (superseded by 0019)
- [0010. Make cluster create provider-agnostic through a ClusterClass topology](0010-provider-agnostic-cluster-create-via-clusterclass.md) (superseded by 0014)
- [0011. Add a first-run setup wizard](0011-first-run-setup-wizard.md) (superseded by 0019)
- [0012. Retire scaling thresholds and rename the GitOps path](0012-retire-scaling-thresholds-and-rename-repo-path.md) (superseded by 0016)
- [0013. What the Cluster API move bought over Terraform](0013-cluster-api-over-terraform.md) (accepted)
- [0014. Narrow horizon to an on-demand pool scaler](0014-narrow-horizon-to-on-demand-pool-scaler.md) (accepted)
- [0015. Narrow horizon to a standalone burst scaler](0015-standalone-burst-scaler-credential-model.md) (superseded by 0017)
- [0016. Make horizon a cluster-agnostic tool with a provider seam](0016-cluster-agnostic-tool-with-provider-seam.md) (superseded by 0018)
- [0017. Replace the CLI burst saga with an in-cluster CapacityLease controller](0017-capacity-lease-controller-over-cli-saga.md) (accepted)
- [0018. Redesign the provider seam around instance lifecycle and capabilities](0018-provider-seam-around-instance-lifecycle.md) (accepted)
- [0019. Replace the terminal interface with a web interface and printer columns](0019-replace-terminal-interface-with-web-and-printer-columns.md) (superseded by 0025)
- [0020. Make Chart.yaml the source of truth for the release version](0020-chart-yaml-as-the-release-version-source-of-truth.md) (accepted)
- [0021. Guarantee teardown with a node-side dead man's switch on two clocks](0021-node-side-dead-mans-switch-on-two-clocks.md) (accepted)
- [0022. Generate cloud-init rather than build a node image](0022-generate-cloud-init-rather-than-build-images.md) (accepted)
- [0023. Observe the armed watchdog from the control plane](0023-observe-the-armed-watchdog-from-the-control-plane.md) (accepted)
- [0024. Validate the release configuration before the tag](0024-validate-the-release-configuration-before-the-tag.md) (accepted)
- [0025. Replace the server-rendered web interface with an embedded single-page application](0025-replace-server-rendered-interface-with-embedded-spa.md) (accepted)
- [0026. Observe node readiness rather than poll for it](0026-observe-node-readiness-rather-than-poll-for-it.md) (accepted)
- [0027. Let the web interface create and release leases, behind a typed writer and a cross-origin guard](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) (accepted)
- [0028. Serve the interface in-cluster behind a verified token and Kubernetes impersonation](0028-serve-the-interface-in-cluster-behind-a-verified-token-and-impersonation.md) (accepted)
