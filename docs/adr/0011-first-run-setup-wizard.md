---
status: accepted
date: 2026-06-16
---

# 0011. Add a first-run setup wizard

## Context

A fresh install was rough. `config.Load()` hard-errored whenever no config existed, so the first run of `horizon` failed instead of helping the operator forward. There was no command to author a config, leaving the operator to copy `config.example.yaml` by hand and guess the pool layout for the cluster in front of them. There was no local install path either, only a source build or `go install`. The config path resolution honoured `HORIZON_CONFIG_DIR` and `~/.config/horizon` but ignored `$XDG_CONFIG_HOME`, so an operator who set the standard XDG variable found their config written somewhere unexpected.

## Decision

A guided `horizon init` Bubble Tea wizard authors the config. It lists the kubeconfig contexts, lets the operator pick one with the current context as the default, connects to that cluster, and queries it to prefill the pool layout: the namespace, the pool-type to MachineDeployment map, and the home cluster name. The operator confirms or edits the fields and picks a theme, and the wizard writes `config.yaml` to the canonical path and reports where. Running bare `horizon` with no config offers the same wizard rather than erroring, then loads the dashboard.

Detection recognises pools by the `horizon.dev/pool-type` label, the documented placement contract, rather than horizon's own managed-by marker, so the wizard sees pools it did not create. The canonical path is XDG-aware: resolution becomes `HORIZON_CONFIG_DIR`, then `$XDG_CONFIG_HOME/horizon`, then `~/.config/horizon`. The chosen context is persisted in a new optional `context` config field, which the `--context` launch flag still overrides. Local install is a `make install` target that builds, copies into `~/.local/bin`, and re-signs on macOS; Homebrew publishing stays deferred.

## Options considered

- A plain non-interactive `init` that writes a stub config. It needs no terminal program, but it leaves the operator to fill the pool layout by hand and learn the cluster's shape elsewhere, the gap this record set out to close.
- Derive prefill from horizon-managed objects only. It reuses the existing managed-by marker, but it sees nothing on a fresh cluster horizon has not yet touched, so the first run prefills nothing.
- Prefill from the `horizon.dev/pool-type` label contract, chosen. The wizard reads the documented label, so it detects operator-authored pools regardless of who created them.
- An offline config template that skips cluster access. It works without a reachable cluster, but it cannot prefill the namespace, pool map, or cluster name, so every field falls to the operator.
- Require cluster reachability for prefill, accepted. The wizard connects and queries, which fills the layout from the live cluster at the cost of needing it reachable during setup.

## Consequences

Setup needs the cluster reachable to prefill, accepted as the price of an accurate layout. The installed binary resolves its config from the canonical XDG-aware path, so it runs from any directory. A new optional `context` field and a new `init` subcommand enter the surface alongside `version` (see [0009](0009-interactive-tui-as-primary-interface.md)); unlike `version`, `init` runs an interactive wizard. Detection reads the `horizon.dev/pool-type` label rather than a managed-by marker, aligning the wizard with the same placement contract the dashboard already uses. Homebrew publishing remains deferred, so the local install path is the `make install` target.
