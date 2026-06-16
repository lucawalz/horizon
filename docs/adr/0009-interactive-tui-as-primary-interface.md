---
status: accepted
date: 2026-06-16
---

# 0009. Make an interactive TUI the primary interface

## Context

The one-shot cobra commands work, but the operator wants a single interactive command centre rather than a scattered set of subcommands. The surface had grown wide: a read-only `status` alongside many small mutating commands (`up`, `down`, `burst`, `cluster`, `backup`, `restore`, `drain`), each its own invocation. Presentation was tangled into the command layer, where rendering, flag parsing, and action logic sat together, even though the underlying client packages were already clean data APIs that returned plain values. The split between observing and acting forced the operator to leave one command and run another to follow up on what `status` reported.

## Decision

A Bubble Tea TUI becomes the primary and only user-facing interface, and running `horizon` with no subcommand launches it.

A new presentation-free `internal/core` package exposes a query surface and a set of action functions. The query surface returns a Snapshot of cluster pressure, nodes, pools, clusters, the nudge state, and autoscaler activity, the same data the old `status` printed, with no formatting. The action functions cover scale up and down, nudge, burst, cluster create, delete, and list, the backup and restore lifecycle, and drain. The TUI renders the Snapshot in a single stacked-panel dashboard and drives every action through the same core functions. Each mutating action sits behind a confirmation, and long operations stream their progress.

The one-shot action subcommands are removed. `version` remains as the only non-interactive subcommand, and `--context` and `--cluster` remain as launch flags. The dry-run, write-to-bedrock, and apply distinctions for cluster creation (previously `--dry-run` and `--write`) become an interactive manifest preview with explicit apply and write actions. No daemon is introduced: the dashboard auto-refresh is a foreground render tick bounded by the program's lifetime, consistent with [0008](0008-retire-watch-daemon-and-wireguard.md). The kubeconfig-only operator, the thin-operator boundary from [0006](0006-cluster-api-operator-pivot.md), and the elastic, reserved, and managed-cluster model from [0007](0007-on-demand-pools-via-machinedeployments.md) are unchanged.

## Options considered

- Keep the CLI and add a separate `tui` subcommand. Both surfaces would coexist, but the command layer would keep its tangled presentation and the operator would still choose between two ways to do the same thing.
- Default to the TUI on an interactive terminal while keeping the subcommands for scripting. It preserves scriptability, but it doubles the maintained surface and keeps the presentation logic in the command layer.
- Replace the one-shot interface entirely, chosen. A single dashboard drives every action through `internal/core`, accepting that scriptability narrows to `version`.

## Consequences

The TUI and the retained `version` command share a single code path through `internal/core`, so observing and acting no longer live in separate invocations and the presentation logic leaves the command layer. Interactive operation is richer: the operator sees pressure, pools, and clusters in one view and acts on them in place, with confirmations on mutation and streamed progress on long runs. Command tests move down to the core query and action layer, where they exercise behaviour without rendering. The usage documentation is rewritten around the dashboard. New Charm dependencies (Bubble Tea, Lipgloss, Bubbles) enter the build. The cost is the loss of arbitrary per-command scripting, since only `version` runs non-interactively, an accepted trade for the home-lab operator workflow this tool serves.
