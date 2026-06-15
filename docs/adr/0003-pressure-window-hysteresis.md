---
status: superseded by 0008
date: 2026-05-02
---

# 0003. Scale on a pressure window with hysteresis, not a single reading

## Context

The watch daemon decides when to add and remove burst nodes from a pressure signal it polls every thirty seconds. Pressure is the higher of cluster CPU and memory utilization, plus a fixed margin when pods are pending, capped at 1.0. That signal is noisy: a momentary spike from a single sample would be enough to provision a cloud node, and a single dip would tear it down again. Bursting costs real money and minutes of provisioning, so the loop must not react to transient noise.

## Decision

The loop keeps a sliding window of recent samples and acts on their average, not on any one reading. Scaling out requires the average to stay at or above the `burst` threshold for several consecutive samples before a burst fires. Scaling in uses a separate, lower `scale_down` threshold, so the levels at which the daemon adds and removes capacity differ. A cooldown after each scaling action, and a cap of `max_burst_nodes`, bound how often and how far it can scale. The window size, both thresholds, and the cooldown are all configured under `thresholds`.

## Options considered

- React to the instantaneous reading against one threshold. Simple, but it flaps: a brief spike provisions a node and a brief dip destroys it, churning cloud resources.
- A sliding window plus hysteresis, chosen. Averaging damps spikes, the consecutive-sample requirement delays commitment, and the split thresholds plus cooldown stop oscillation around a single level.

## Consequences

The daemon reacts to sustained pressure rather than to noise, and the gap between the burst and scale-down levels keeps it from oscillating. The trade is latency: a genuine sustained spike waits out the window and the consecutive-sample count before a node appears. Every knob is exposed in config because the right values depend on the cluster and the workload, not on a default that suits all of them.
