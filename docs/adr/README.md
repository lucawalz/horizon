# Architecture Decision Records

These records capture horizon's own design decisions, the choices behind the Go orchestrator that drives bursts. They follow the [MADR](https://adr.github.io/madr/) format. The infrastructure decisions horizon builds on, what a burst node is and how the cluster is backed up, live in the companion [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.

- [0001. Drive bedrock's Terraform module instead of owning cloud IaC](0001-drive-bedrock-terraform.md) (accepted)
- [0002. Put cloud calls behind a provider interface](0002-pluggable-provider-interface.md) (accepted)
- [0003. Scale on a pressure window with hysteresis, not a single reading](0003-pressure-window-hysteresis.md) (accepted)
- [0004. Migrate a workload with a backup, an affinity rewrite, and an eviction](0004-velero-workload-migration.md) (accepted)
- [0005. Record burst progress as a resumable phase state machine](0005-resumable-phase-state-machine.md) (accepted)
