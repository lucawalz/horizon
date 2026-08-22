---
status: accepted
date: 2026-08-22
---

# 0026. Observe node readiness rather than poll for it

## Context

The lease controller registered for `CapacityLease` objects only. `SetupWithManager` was `For(&v1alpha1.CapacityLease{})` with no `Owns` and no `Watches`, so a burst node going Ready produced no reconcile of the lease that owns it. While a lease waited, `reconcileNodes` returned `RequeueAfter: leasePollInterval`, an unexported thirty-second constant, and `Status.ReadyAt` was stamped `r.now()` at the moment the requeue timer fired. The recorded readiness instant was therefore the timer, not the event.

That has a measured consequence, and the way it was found is the reason this record exists. The measurement campaign reported in [evaluation.md](../evaluation.md) has thirty burst runs of time to ready. The distribution is bimodal, nineteen runs at 60 or 61 seconds and eleven at 90, 91 or 94, and every one of the thirty values lands within one second above a multiple of thirty. That bimodality was carried as a physical finding for two sessions, described as two populations of boot behaviour, before anyone checked the instrument that produced it. The check is one table. The Hetzner-side timings from the same artefacts are unimodal and not quantised at all: `initializing` at a median of 6 seconds, `starting` at 12, `running` at 22, each with a tight range. The gap from `running` to recorded ready is 34 to 45 seconds in the fast group and 62 to 69 seconds in the slow one, with nothing in between. A physical process that produces a thirty-second gap with no intermediate values, on an instrument whose grid is exactly thirty seconds, is the grid.

The second symptom has the same cause. `reconcileNodes` already distinguished three waiting stages and discarded each one with a bare `continue`: the instance entry is not yet `Created`, so no server exists at the provider; the entry is `Created` but `matchNode` returns nil, so the server exists and no `Node` object has registered; a `Node` exists but is not Ready, so k3s joined and the kubelet has not come up. All three collapsed into a single `InstancesReady=False` with reason `WaitingForNodes` and the message `%d of %d nodes ready`. `nodeRegistrationTimeout` is fifteen minutes, and when it fires the lease jumps to `Degraded` with the machine already released. For those fifteen minutes the entire diagnostic surface was one unchanging count, and the operator could not tell a provider that had not created a server from a machine whose cloud-init had failed.

## Decision

**A readiness timestamp is sourced from the transition that caused it, not from the observation that noticed it.** That is the decision. The watch is the mechanism that makes the observation prompt, and it is subordinate to the timestamp rule rather than a substitute for it.

`Status.ReadyAt` is the latest `NodeReady` `lastTransitionTime` across the instances that have joined, since a lease becomes ready when its slowest node does. Two guards fall back to the reconcile clock rather than record something silently wrong: a zero transition time, and a transition time that precedes `Status.AcceptedAt`. The second is not hypothetical. A burst node's clock can disagree with the control plane's, and the resulting negative duration would poison the `horizon_lease_ready_seconds` histogram, which has no way to represent it. Both guards are pinned by tests at the level of the function that applies them, because the ordering invariant that makes one of them redundant in the running controller is the caller's invariant and not the function's.

This rule holds on its own merits and survives an operator restart. It does not depend on the watch. What the watch buys is that the transition is noticed within the round trip of an informer event instead of within the requeue period, so the reported instant is fresh rather than merely correct in retrospect.

### The watch has to see a node before it is adopted

`SetupWithManager` gains a `Watches` on `corev1.Node`. The map function's hard case is a node that has just registered and has not yet been adopted. `horizon.dev/lease` is applied by `patchNodeMarks` during adoption, so the node that most needs to wake a reconcile is exactly the one that does not carry it. What it does carry is `horizon.dev/pool`, written by cloud-init and enforced at render time, and that is also the label the manager cache filters `corev1.Node` on, so every burst node is in the cache from the moment it registers whether adopted or not.

The map function enqueues the named lease when the adoption label is present, and otherwise lists `CapacityLease` objects and enqueues any whose `Status.Instances` hold a matching entry. The match uses the same rule `matchNode` uses, provider ID first and then name, extracted into one predicate so the two cannot drift apart. `CapacityLease` is cluster-scoped, so the enqueued request carries a name and no namespace.

The thirty-second requeue stays, demoted from the primary mechanism to a safety net for whatever the watch misses.

### Waiting is reported by stage, as an observation

Each instance entry carries a `Stage`, one of `AwaitingInstance`, `AwaitingRegistration`, `AwaitingReady` or `Ready`, derived at each of the points that previously discarded the distinction. The stage refines the existing `Phase` and does not replace it: `Phase` records lifecycle ownership and keeps its meaning. An entry that is draining or released names no stage, because it has stopped working towards readiness, and a lease whose only remaining entry is a retired one still reports `WaitingForNodes` exactly as before.

The per-instance stages aggregate to the least advanced one, which picks the `InstancesReady=False` reason and message. The message names the blocking instance and how long it has been waiting, computed from `entry.CreatedAt`, which is the provider's own creation timestamp and is already the input to the fifteen-minute registration timeout. It also keeps the `%d of %d nodes ready` count, which is what the web interface and the existing tests read.

The message states the observation and stops there. Nothing inside the machine is observable from the control plane: there is no console read and no cloud-init probe. `AwaitingRegistration` persisting past a few minutes is consistent with a failed cloud-init or a failed k3s join, and it is equally consistent with a slow boot, so the message says that no node has registered and does not name a cause.

No printer column is added. A per-instance stage cannot be expressed in a printer column without an array index, and an index would report the first replica's stage as though it were the lease's on any multi-replica lease. The aggregate is available as the reason on the `InstancesReady` condition, which the existing `Ready` column already points at, so the printer columns are left alone.

### The poll interval becomes a declared instrument

`leasePollInterval` was an unexported constant with no flag, no field on `manager.Options`, no schema field and no chart value. It is now `controller.DefaultPollInterval` with a `PollInterval` field on the reconciler and on `manager.Options`, a `--lease-poll-interval` flag, and a `lease.pollInterval` chart value that renders it the same way `--metrics-bind-address` is already rendered. A zero value means the default, so a caller that sets nothing is unaffected. The point is not that the value needs changing. The point is that a measurement instrument that governs a published number should be visible in the deployment that produced it.

### The histogram is re-cut

`horizon_lease_ready_seconds` used a fifteen-second grid, which loses sub-thirty-second resolution a second time even once the poll grid is gone. The buckets are now 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300 and 600 seconds, so the region the fix opens up is actually resolved.

## Options considered

- Shrink `leasePollInterval`. Rejected: it trades API server load for resolution and leaves the reading a poll artefact at a finer grid. The bimodality would become less obvious without becoming less wrong.
- Use `Owns(&corev1.Node{})` rather than `Watches`. A `Node` is created by its own kubelet, is cluster-scoped, and is adopted by a lease after the fact. There is no owner reference and there could not be one.
- Match nodes in the watch by the adoption label alone, and skip the list. Rejected: the label is written during adoption, so the case the watch exists to catch, a node that has just registered, does not carry it.
- Derive `ReadyAt` from the provider's creation timestamp plus a boot estimate. That is a model, not an observation, and it would reintroduce the same class of error the measurement exposed.
- Drop the requeue once the watch exists. Rejected: the expiry deadline, the watchdog renewal and both stall timeouts run off the same requeue, and a watch that misses an event would leave a lease holding billable capacity with nothing scheduled to notice.
- Recompute the published time to ready figures from the existing artefacts. Not possible. The artefacts record `readyAt` as the controller wrote it, so the quantisation is baked into the data rather than into the analysis.

## Consequences

The published dataset stays as published. It was gathered under the poll and is quantised to thirty seconds, and this change does not retrospectively improve it. Section 3.2 of [evaluation.md](../evaluation.md) already states the defensible reading, that true time to ready lies in (30, 60] seconds for nineteen of thirty runs and in (60, 90] for eleven, and the instrument cannot say where inside those intervals. The medians and the null result on location are unaffected, because the same grid applied to every run. Any future measurement taken after this change is taken on a different instrument, and comparing the two directly compares instruments as much as machines.

Continuity with previously scraped `horizon_lease_ready_seconds` series is broken by the bucket change. Prometheus retention on the estate is seven days and the campaign's data is already exported to artefacts, so nothing that matters is lost.

The `InstancesReady=False` message now carries an elapsed figure, so it changes on most reconciles while a lease is waiting, and each change is a status write. That window is bounded by the fifteen-minute registration timeout, and the elapsed figure is only as accurate as the reconcile that wrote it, which is the second reason the watch matters.

The lease controller now wakes on every `Node` event in the reserved pool, and for an unadopted node that means one `CapacityLease` list per event. The manager cache already restricts `corev1.Node` to objects carrying `horizon.dev/pool`, so the event rate is the burst fleet's, not the cluster's.

What is still invisible is unchanged. The stage says which side of the boundary a machine is on, not what is happening inside it. The `horizon.dev/watchdog-armed` annotation from [0023](0023-observe-the-armed-watchdog-from-the-control-plane.md) remains the only in-machine signal the control plane gets, and it arrives only after the node has joined, which is after the stage that most often stalls.

The wider lesson is the one the campaign taught rather than anything in this code: an implementer's report is a claim and an artefact is evidence. A bimodal distribution whose modes sit exactly one sampling period apart is a statement about the sampler until something independent of the sampler says otherwise.

## Supersession

None. This extends [0017](0017-capacity-lease-controller-over-cli-saga.md), which stays `accepted`. It changes how the controller observes node readiness and how it reports the wait; it replaces no part of the lease lifecycle that record defines.
