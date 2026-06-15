---
status: accepted
date: 2026-06-15
---

# 0008. Retire the laptop watch daemon and WireGuard

## Context

The old design scaled from a laptop. A `watch` daemon polled Prometheus on an interval, kept a sliding window of pressure samples with hysteresis, and decided when to add and remove burst nodes (see [0003](0003-pressure-window-hysteresis.md)). That loop only ran while the laptop ran, and it duplicated a control loop the cluster can host natively. Connectivity used a self-hosted WireGuard hub on the home router, with horizon registering each burst node as a peer over SSH. The CAPI substrate in bedrock (see [0006](0006-cluster-api-operator-pivot.md)) brings both an in-cluster cluster-autoscaler and Tailscale, which cover what the daemon and the WireGuard hub did.

## Decision

The watch daemon is removed. Scale-on-demand is delegated to the in-cluster cluster-autoscaler, which watches for pending pods and scales the autoscaler-managed MachineDeployments inside the cluster, independent of any laptop. horizon keeps only the explicit commands (`up`, `down`, `burst`, `cluster`), and `status` reports the autoscaler's activity from its status ConfigMap and the cluster pressure header for operator visibility, read-only. WireGuard is removed entirely. Connectivity to the cluster and between nodes is Tailscale, configured in bedrock, and horizon reaches the cluster through a kubeconfig context rather than an SSH-managed overlay. The pressure window and hysteresis configuration is retained only as a `status` display threshold, not as a scaling control loop.

## Options considered

- Keep the laptop watch loop alongside CAPI. It would duplicate the in-cluster autoscaler and only act while the laptop is up, leaving the cluster unable to scale on its own.
- Delegate scale to the in-cluster autoscaler and drop WireGuard for Tailscale, chosen. The scaling loop runs where the cluster runs, and connectivity is managed by the substrate.

## Consequences

Scale-out no longer depends on a process on the laptop, and the cluster scales on pending pods on its own. horizon becomes a tool for explicit actions and read-only observation rather than a long-running daemon. The pressure-window machinery survives only as a display aid, so its retained config knobs no longer drive any decision. This record supersedes [0003](0003-pressure-window-hysteresis.md). Manual pools that horizon scales directly deliberately omit the autoscaler min and max annotations, which keeps them out of the autoscaler's control, so the two scaling paths do not fight. The Tailscale and autoscaler configuration lives in the [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.
