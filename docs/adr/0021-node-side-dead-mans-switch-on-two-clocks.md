---
status: accepted
date: 2026-08-03
---

# 0021. Guarantee teardown with a node-side dead man's switch on two clocks

## Context

[0018](0018-provider-seam-around-instance-lifecycle.md) already records the constraint this decision answers. Hetzner bills for a server while the server object exists, powering off does not release the reserved capacity, and the provider offers no server-side deadline. Self-destruction therefore requires an API call, and an API call made from the server requires a delete-capable credential on an ephemeral machine. That is the premise here rather than a new finding.

Every teardown path horizon had before this change runs inside the operator. The finalizer, the confirmed delete, the launch and registration timeouts and the orphan collector are independent of each other in the sense [0017](0017-capacity-lease-controller-over-cli-saga.md) intended, and each closes a different race. They nonetheless share one dependency: all four need the operator process, the API server and the network at the same moment. That is the combination most likely to fail together, and it is the combination a leaked server usually implies. Adding a fifth cluster-side layer raises the chance that something notices in the ordinary case and leaves the class of failure untouched.

The custom resources already carried the vocabulary for the missing layer without the mechanism behind it. `ProviderConfig.spec.watchdog` declares `renewInterval`, `slack` and `maxLifetime`, cross-validated against each other by the schema, and `CapacityLease.status.watchdogDeadline` is declared in the lease status. Nothing reads the first and nothing writes the second.

## Decision

Run a dead man's switch on the leased server itself, armed when the machine boots and fired by the earlier of two independent clocks.

The first clock is a renewable wall-clock deadline that the operator publishes as an annotation on the server's own `Node` object. The second is a monotonic backstop at `maxLifetime`, measured from the moment the agent starts. The effective fire time is the earlier of the two, so the annotation may only shorten the remaining life and never defer it. The backstop reads Go's monotonic clock, which a wall-clock step cannot move, so correcting or tampering with the system time cannot buy a server more time than `maxLifetime`.

The deadline is pulled rather than pushed. The agent reads its own `Node` object with the kubelet credential the machine already holds, and asks for nothing else. Measured against the live cluster, that identity can read its own `Node` and its annotations, is refused every other node and every collection list, and cannot delete its own `Node` object. The design therefore needs no new Kubernetes credential and no inbound reachability to a firewalled machine, and the node cannot deregister itself. It can only destroy the server it runs on.

A node that cannot reach the cluster is treated as dead, deliberately. It is not running scheduled work and it is pure cost, so the switch fires. There is no partition detector and no retry budget to tune, because the retry budget already exists: `slack` is the margin the schema requires beyond `renewInterval`, and the two are cross-validated against each other and against `maxLifetime` when the `ProviderConfig` is admitted.

Deadlines are decimal Unix seconds wherever they are written, reusing `provider.FormatExpiry` and its `ParseExpiry` counterpart. The encoding exists because Hetzner label values reject `:` and `+`, which rules out RFC 3339, and there is no reason for the annotation to disagree with the provider label. One encoding, one parser.

The switch ships as a third subcommand of the existing binary, `horizon watchdog`, implemented in `internal/agent`. The name is the one the schema already uses, so the flag, the field and the process are called the same thing.

The delete-capable token reaches the machine through `nodeCredentialSecretRef`, the optional Secret reference [0018](0018-provider-seam-around-instance-lifecycle.md) put on the Hetzner provider block, resolved and rendered into user-data when the provider is built. It is never an inline value, per the same record. Wiring that field also supplies the input that `Capabilities().SelfTerminationStopsBilling` was meant to gate: a provider that reports false and has no node credential configured refuses to provision, which is the fail-closed behaviour [0018](0018-provider-seam-around-instance-lifecycle.md) specifies and which had never been implemented.

Delivery is cloud-init first. The agent is fetched and started by the user-data that boots the machine, and is baked into the node image only once the semantics have settled and stopped changing.

## Options considered

- Push the deadline to the node over SSH or a small API on the machine. Rejected: it requires the operator to be alive at exactly the moment it might not be, which is the failure this layer exists to survive, and it requires inbound reachability to a firewalled machine.
- Rely on a provider-side TTL. It does not exist on Hetzner. AWS ends billing on an in-guest shutdown with `InstanceInitiatedShutdownBehavior` set to terminate, and Google Compute Engine enforces `maxRunDuration` with `instanceTerminationAction` set to delete; both are server-side and neither needs a credential on the machine. That three-tier asymmetry, recorded in [0018](0018-provider-seam-around-instance-lifecycle.md), is why this record exists for Hetzner alone.
- Keep the guarantee cluster-side only. The status quo, and a single point of failure by construction. The measured baseline in [0017](0017-capacity-lease-controller-over-cli-saga.md) was a leaked server on five of five `SIGKILL` runs.
- Ship a separate `horizon-agent` binary. Rejected: a second release artifact, a second version to keep in step with the operator, and duplicate copies of `FormatExpiry` and the Hetzner client. GoReleaser already publishes a static linux/amd64 tarball and a checksums file from the one build.
- Embed the agent in user-data. Hetzner caps `user_data` at 32 KiB and the stripped static binary measures 35 MB, three orders of magnitude over.
- Self-terminate by in-guest shutdown, as on AWS. Hetzner keeps billing a powered-off server, so this stops nothing.
- Give the node a delete-capable Kubernetes credential so it can remove its own `Node` object. NodeRestriction refuses a kubelet delete on its own node, measured, and deregistering would not stop the bill in any case.
- Run one clock instead of two. The wall clock alone is defeated by a clock step or an edited annotation. The monotonic clock alone cannot express a renewable lease, only a fixed maximum.
- Fall back to a local `poweroff` after repeated delete failures. Rejected twice over: it does not stop Hetzner billing, and it kills the only process still in a position to fix the problem.

## Evidence

The design was proven on real hardware on 2026-08-03. A leased server deleted itself at its deadline with the operator scaled to zero, the cluster unreachable from the node, and the node's wall clock stepped back three hours. A second run started the agent with an invalid token: it failed the startup identity proof and exited non-zero at boot, rather than arming and reporting a guarantee it could not keep.

## Consequences

A delete-capable project token now lives on an ephemeral machine, so the dedicated-project mitigation recorded in [0018](0018-provider-seam-around-instance-lifecycle.md) becomes load-bearing rather than advisory. It is not in place today. The token is scoped to the shared project and is the same token the operator uses, which is the honest state of the deployment. The reasoning for accepting that for now is recorded rather than assumed: the project holds no other servers, `Delete` refuses any instance that does not carry `horizon.dev/managed-by=horizon`, and the node image is reproducible from a content hash, so rebuilding it elsewhere is cheap. Moving to a dedicated project means rebuilding the image there rather than transferring it, because Hetzner supports moving a snapshot between projects only as a Console action and exposes no API for it.

A partitioned but otherwise healthy node is destroyed. That is intended and it is the price of treating unreachability as death. It also means the switch must never run on a machine that is not ephemeral, which is enforced by the same ownership guard: `Delete` refuses an instance without horizon's management label, so an agent pointed at anything else fails rather than destroys.

The backstop cannot be renewed, so no lease can outlive `maxLifetime` regardless of what the annotation says. The schema already caps a lease `duration` at 8h and `maxLifetime` at 24h, so the bound is consistent with the resources by construction rather than by convention.

There is no delete dry-run at Hetzner, so the startup identity proof is a `Get` and a read-only token passes it. Such a token arms a switch that then fails at the deadline, retrying until the process dies. The mitigation is operational rather than technical: mint the node token with read and write.

Until the agent is baked into the node image, cloud-init is a single point of failure for installing it. A machine whose `cloud-final` never runs has no switch, and falls back to the operator-side guarantee. That is the previous baseline rather than a regression, but it is the reason baking the agent into the image is the next step and not an optimisation.

An agent restart resets the monotonic backstop. A sentinel file written before the first delete call makes a restart resume the teardown instead of arming a fresh lifetime, so the window is narrow: only a crash before the switch fires resets the clock. This is accepted, because a crash-looping agent is a loud symptom that the operator-side layers still see.

The watchdog's delete retries back off from 5 seconds, doubling, capped at a minute apart, and continue for as long as the process lives; giving up leaves the server billing. Identity is proved by reading the instance back exactly as the controller does, and the provider client the watchdog builds carries no server-create specification, so it can delete but never create.

This record extends [0018](0018-provider-seam-around-instance-lifecycle.md) and supersedes nothing. It adds the layer that record's capability reporting was designed to describe, and it implements the refusal that record specified.

## Update 2026-08-24

"The schema already caps a lease `duration` at 8h and `maxLifetime` at 24h, so the bound is consistent with the resources by construction rather than by convention" was wrong. The two caps are ceilings, not a relation, and the schema validates each field against its own bounds and never against the other. A `ProviderConfig` may set `maxLifetime` as low as 5m while a `CapacityLease` against it asks for 8h, and both are admitted. The lease then outlives the machine: the server destroys itself at the backstop while the cluster still reports the lease as live.

The rule is cross-resource, so CEL cannot express it, and there is no admission webhook in the repository to hold it either. The controller therefore derives `status.expiresAt` on every reconcile as the earlier of the requested deadline and the backstop, rather than stamping it once at acceptance, and reports an `ExpiryClamped` condition when the backstop is the one that holds. Clamping is preferred over refusing the lease, because the failure worth eliminating is a silent overrun: after the clamp, what the cluster believes matches what the machine does.

The backstop the controller computes is not the one the agent runs. The agent measures `maxLifetime` from its own process start, which the cluster cannot observe, so the controller anchors on the earliest instance's creation instead. That is deliberately conservative, because creation precedes agent start.

A second gap is recorded here rather than closed. The `horizon.dev/expires-at` label is written once in the create call and the provider seam offers no way to update it, so it goes stale on a lease whose deadline later moves. An agent reads it at start to seed the wall clock before its first node read, and the node annotation supersedes that seed on the first successful poll, so a stale value governs for at most one poll interval.
