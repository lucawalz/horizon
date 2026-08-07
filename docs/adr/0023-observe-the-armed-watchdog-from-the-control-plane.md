---
status: accepted
date: 2026-08-07
---

# 0023. Observe the armed watchdog from the control plane

## Context

On 6 August 2026 a burst node's cloud-init failed to install the watchdog: `curl` was missing from the runcmd PATH on NixOS, `cloud-final.service` failed loudly, and nothing consumed the failure. [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) armed a dead man's switch on the leased server itself for exactly the reason that layer needs to survive an unreachable control plane, but on this node the switch never armed. The node joined, ran workloads, and reported Ready, all while carrying no working watchdog.

The registration timeout that exists to catch a node that never joins did not fire, because it never fires for this failure. [0022](0022-generate-cloud-init-rather-than-build-images.md) renders the estate with `--install-kubernetes=false`, and the node image orders `k3s.service` merely `after` `cloud-final.service` rather than requiring it, so a runcmd that aborted mid-script still left k3s free to start and join. Every existing signal that a node had joined kept saying yes.

A prior fix closed half the gap: an in-progress install marker written before the watchdog install and removed after, so a crash mid-install leaves local evidence instead of silence. Reading that evidence means reaching the node, and burst nodes are meant to carry no SSH key; the estate's `ProviderConfig` still carries a temporary `sshKeys` entry that exists only for as long as there is no other way to check. The 6 August incident is precisely the case where nothing was reading that evidence. The layer meant to survive an unreachable control plane needed its own visibility from a place that does not require reaching the node at all.

## Decision

The agent annotates its own `Node` object with `horizon.dev/watchdog-armed` once armed, and refreshes it on every poll tick that does not fire the switch (`internal/agent/agent.go`, `internal/agent/node.go`). The controller reads that annotation for every instance in `InstancePhaseJoined` and raises a `WatchdogArmed` condition on the owning `CapacityLease`: True when every joined node reports a fresh annotation, False with a `WatchdogUnarmed` warning event naming the offending nodes otherwise (`internal/controller/capacitylease_watchdog_armed.go`). A printer column surfaces the condition on `kubectl get capacityleases` (`api/v1alpha1/capacitylease_types.go`), and because the condition lives in `status.conditions` beside `InstancesReady`, it is shaped the same way kube-state-metrics' generic conditions handling already turns a CRD condition into a series the estate can alert on.

The condition reports; it never acts. It does not gate scheduling, block a lease from progressing, or trigger teardown. Teardown stays exactly where it already was: the orphan reconciler sweeps expired instances on its own schedule regardless of what this condition says, and the node-side switch from 0021 still fires on its own two clocks when it is armed. A missing watchdog is not a missing teardown guarantee. The orphan reconciler does not depend on it. Losing the watchdog is a loss of the layer that survives the control plane being unreachable, which is a loss of defence in depth, not a loss of teardown.

### Two annotations, two encodings, on purpose

`horizon.dev/watchdog-deadline`, from 0021, carries decimal Unix seconds, because that value is also written as a Hetzner instance label and Hetzner label values reject `:` and `+`. `horizon.dev/watchdog-armed` carries RFC 3339, because it is read only inside the cluster, never becomes a provider label, and readability wins where the other constraint does not apply. `internal/provider/provider.go` gives each key its own pair of functions rather than sharing one codec: `FormatExpiry`, `ParseExpiry`, and `ParseExpiryValue` own the deadline key's decimal-seconds encoding; `FormatArmed` and `ParseArmedValue` own the armed key's RFC 3339 encoding. Keeping the two pairs separate means a later refactor cannot quietly harmonise the encodings; they disagree because the two consumers do, and that disagreement is the point.

### The staleness window has a ceiling

The controller judges an armed annotation stale after `policy.RenewInterval * 3` (`watchdogArmedStalenessRenewIntervalMultiple`), the same multiple 0021 already uses when it renews the wall-clock deadline. `RenewInterval` is bounded loosely by the schema, with `renewInterval + slack` capped at one hour and `slack` required to exceed `renewInterval`, which alone would let the staleness window run past an hour. That is wider than the property is meant to bound, so the window is clamped to `watchdogArmedStalenessCeiling`, three times `agent.DefaultPollInterval`, the agent's compile-time default poll interval of 15 seconds. The ceiling is 45 seconds today, and for any `renewInterval` above 15 seconds it, not the renew-interval multiple, is what actually governs.

## Options considered

- Write a Hetzner instance label from the node. Rejected twice: it needs `curl`, the tool whose absence caused the incident, and hcloud's label PUT replaces the whole map, so a shell writer would clobber `horizon.dev/expires-at` and `horizon.dev/lease-uid` on the same instance, breaking the watchdog's own wall-clock seed and the orphan sweep in the same write.
- Set a node label instead of an annotation. NodeRestriction confines a kubelet's self-set labels to an allowed set of prefixes and refuses `horizon.dev/`. Annotations carry no such restriction, and the Node authorizer already lets a node patch its own `Node` object, the same permission 0021 already relies on to read the deadline.
- Rely on the node-side install marker alone. Reading it means reaching the node, and 6 August is precisely the incident where nothing was reaching it.
- Rely on the existing registration timeout. It does not fire for this failure: the estate renders with `--install-kubernetes=false`, and the node image orders `k3s.service` merely `after` `cloud-final.service` rather than requiring it, so the node joins and reports Ready even when the runcmd that would have armed the watchdog aborted first.

## Consequences

An unarmed node becomes visible without an SSH session, ahead of removing the temporary `sshKeys` entry the estate's `ProviderConfig` still carries.

Absence detection covers more than a failed download. A crash-looping unit, a script that never started, and an install that died before the marker could be written all read the same way from the controller's side: no fresh annotation, condition False. The controller does not need to distinguish the cause to raise the signal.

The staleness window is bounded rather than open-ended, so a large `renewInterval` cannot leave the signal quiet for as long as the raw multiple would otherwise allow.

A known limitation is recorded rather than hidden. The agent's poll interval is not currently threaded into the generated watchdog unit: `internal/cloudinit/watchdog.go` emits `--max-lifetime` on the `ExecStart` line but no `--poll-interval`, so every deployed agent runs at its compile-time default regardless of what an operator might otherwise configure. The staleness ceiling is keyed to that same compile-time default, `agent.DefaultPollInterval`, rather than to a value the controller actually observes, which stays coherent only because nothing today makes the two diverge. Emitting the poll interval through the watchdog policy, the way `--max-lifetime` is already emitted through `provider.MaxLifetimeSentinel`, would let the ceiling track the value it is meant to bound instead of a constant that happens to match it. That wiring is the known follow-up, not done here.

The annotation reaches a node only once the deployed operator's release carries this code, because the version substituted into the rendered cloud-init resolves `${HORIZON_VERSION}` to the operator's own build. A cluster running an older operator against a newer node image, or the reverse, sees no `WatchdogArmed` condition at all rather than a wrong one, until both sides run a release that has it.

## Supersession

None. This extends [0021](0021-node-side-dead-mans-switch-on-two-clocks.md), which stays `accepted`. It adds visibility for a mechanism 0021 already built; it replaces no part of it.
