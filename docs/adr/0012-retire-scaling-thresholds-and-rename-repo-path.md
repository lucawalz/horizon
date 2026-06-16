---
status: accepted
date: 2026-06-16
---

# 0012. Retire scaling thresholds and rename the GitOps path

## Context

The `thresholds` config block (`burst`, `scale_down`, `window`, `cooldown_minutes`, `max_burst_nodes`) is a remnant of the watch-daemon scaling loop retired in [0008](0008-retire-watch-daemon-and-wireguard.md). The in-cluster cluster-autoscaler owns scaling now, so only `burst` was still read, and only to color the dashboard CPU and memory gauges. The setup wizard also wrote zeroed thresholds, which would have mis-colored every gauge. Separately, the `bedrock_path` field name assumed the companion repo is named bedrock, at odds with horizon being provider-agnostic.

## Decision

The entire threshold config is removed. The dashboard colors the CPU and memory gauges from fixed usage bands, yellow at 75 percent and red at 90 percent, rather than from any configured score. The `bedrock_path` field is renamed to `repo_path`, and a non-empty retired `bedrock_path` is rejected at load time alongside `infra_path`.

## Options considered

- Keep the thresholds but seed sane defaults so the wizard stops writing zeros. It preserves the schema, but it keeps a block no operator tunes and that no longer governs scaling.
- Remove the thresholds entirely, chosen. The config shrinks to what horizon actually reads.
- Keep gauge coloring configurable. It offers a knob, but no operator tuned it, so the configurability is unused weight.
- Color the gauges from fixed usage bands, chosen. The display cue is consistent and needs no config.
- Leave the `bedrock_path` name. It matches the homelab instance, but it bakes a specific companion repo into a provider-agnostic tool.
- Rename to a generic `repo_path`, chosen. The field names the GitOps work tree without naming a particular repository.

## Consequences

The config shrinks and the wizard no longer writes meaningless values. Gauge coloring is consistent and no longer tunable, an accepted trade for a display cue. An existing config carrying a `thresholds` block or an empty `bedrock_path` still loads, because unknown keys are ignored, while a non-empty `bedrock_path` now fails fast with a clear message that points at `repo_path`. The setup wizard from [0011](0011-first-run-setup-wizard.md) writes the renamed field and no thresholds.
