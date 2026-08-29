---
status: accepted
date: 2026-08-02
---

# 0017. Replace the CLI burst saga with an in-cluster CapacityLease controller

## Context

Burst orchestration lives in `core.Burst` as a linear function guarded by two deferred rollbacks. The outer one returns the reserved server count to its prior value if anything after the scale step fails; the inner one restores saved affinity and tolerations if the workload migration fails. Both run on a fresh context so that a cancelled burst still unwinds. Within its own assumptions the design is correct.

Those assumptions do not survive process death. A deferred function runs when a function returns, not when a process is killed, so a `SIGKILL` between the scale and the completion leaves rented servers running and billed with nothing left that knows they exist.

This was measured before changing anything, against the real `core.Burst` and the production node image, one server per run. Under `SIGKILL` during the node wait, five of five runs leaked a server. Under a clean failure, letting the five minute node-ready timeout expire, zero of one runs leaked: the rollback fired and returned the pool to its prior count.

The control result is the important one. The rollback is not missing and it is not wrong. It is cleanup that cannot survive the class of failure it exists to handle, which is a property of where it lives rather than of how it is written. No amount of care inside a linear function fixes it, because the function is not running when the failure happens.

Two related defects share the same root. Nothing deletes the Kubernetes `Node` object when a server goes away, so scale-down strands node records; the workaround is a CronJob in bedrock that deletes any reserved node whose Ready condition is not true, which races a node that is still joining. And orchestration is reachable only through the terminal prompt, so there is no way to invoke a burst from a script, from CI, or from a test. Measuring the leak above required writing a throwaway `main` that calls `core.Burst` directly.

## Decision

Move orchestration into an in-cluster controller and express a burst as a declarative resource with a deadline.

A `CapacityLease` custom resource carries the request: how much capacity, from which provider and region, for how long, and for which workload. The controller reconciles the resource against observed reality on every pass rather than executing a sequence once. Compensation stops being a deferred call and becomes the ordinary business of a level-triggered loop, which by construction runs again after a crash.

Crash safety rests on four independent layers, taken from Karpenter rather than invented here, because the published failure history is instructive: every documented leak in Karpenter and Cluster API came from identity mismatch rather than from absent cleanup code.

The finalizer is added and persisted in its own reconcile pass before any provider call. Provider-side labels tying the instance to the lease are applied atomically in the create call, never as a subsequent tagging step, so an instance is never anonymous even for an instant. A garbage collector lists every configured provider on a slow tick and deletes anything whose owning lease is gone, skipping any instance whose `Node` still reports Ready, because the kubelet is a more reliable witness than an eventually consistent cloud API. Coverage follows configuration rather than assuming a single provider: an instance created under a second provider configuration and swept by neither would be a permanent silent bill, which is the failure this component exists to prevent. Launch and registration timeouts bound the case where a machine boots and never joins.

Instance identity is the provider's own identifier recorded in status, paired with the lease UID written into a provider label, so the mapping can be recovered from either side. The UID rather than the name, because a name can be reused by a recreated lease and the whole point of this record is that identity must not derive from anything mutable. Instance names are likewise not derived from anything that can change.

Deletion is treated as complete only when the provider reports the instance absent. A successful delete call is not evidence.

Three ordering invariants hold the design together and none of them is apparent from reading the code that implements them, so they are recorded here rather than beside it. The garbage collector lists provider instances before it lists leases, so that a lease created part way through a sweep still shields the capacity it owns; reversing those two reads reintroduces the race the collector exists to close. The deadline runs from acceptance rather than from object creation, so a lease that waits in the queue is not silently shortened. And a slot retired by a launch or registration timeout keeps its error and is never refilled, which departs from Karpenter deliberately: relaunching on failure would let an eight hour lease with a broken image cycle dozens of billable machines, so one attempt per ordinal and a loud degraded condition is the better trade.

The deadline is written as a provider-side label on the instance itself, so an expired resource remains classifiable by any observer even if the lease object, the controller, or the whole cluster is gone.

Saved workload placement moves out of process memory and onto the workloads themselves, as an annotation applied in the same patch that rewrites affinity. Restore reads the annotation. This survives operator loss, operator removal, and deletion of the lease, and it means recovery does not require the lease to exist.

Orphan reconciliation absorbs the bedrock CronJob, which is then deleted. The reconciler deletes a `Node` when its owning lease is gone and every configured provider reports the instance absent, and never on the Ready condition, which removes the race against a joining node. Nothing records which provider owns a given `Node`, and by the time this path runs the lease that knew is already gone, so absence cannot be attributed to one provider and must be proven against all of them. Two consequences follow deliberately. With no provider configured there is no witness at all, so nothing is deleted. And a provider that cannot be built makes absence unprovable, so the `Node` survives and the work requeues. A stranded `Node` object costs nothing; deleting a live one because configuration briefly disappeared costs a great deal.

Phase appears in status for readability and is never used as control flow. [0005](0005-resumable-phase-state-machine.md) already recorded that a phase state machine is the wrong shape for this problem, and that finding stands; a derived phase is a display field, not a resumption mechanism.

## Options considered

- Keep the saga and harden it. Persist progress to a file, or trap signals and unwind. Rejected: a signal handler does not run on `SIGKILL`, a state file is a third store that can disagree with both the cluster and the provider, and neither survives the machine going away. The measurement shows the failure is structural rather than a gap in coverage.
- Implement Karpenter's `CloudProvider` interface and import it as a library. This yields the resource model, the lifecycle machine, the finalizer, the garbage collector and expiry without writing them. Rejected on two grounds: a Hetzner provider for Karpenter already exists, so the path is largely occupied, and Karpenter's core is demand-driven, bringing a scheduler, bin-packing, price-aware instance selection and consolidation that a duration-bounded lease does not need. The mechanisms are worth copying; the dependency is not.
- Return to Cluster API, which models machines and their lifecycle properly. Rejected as already tried and retired. Its model assumes ownership of the cluster lifecycle including the control plane, and the surrounding weight of providers, contracts and management-cluster separation is disproportionate for adding capacity to a cluster that already exists.
- Keep orchestration in the command line but add a headless invocation so it can at least be scripted and tested. Rejected as insufficient: it addresses the testability defect and leaves the leak untouched, since a scripted process can be killed exactly as an interactive one can.

## Consequences

A burst becomes a resource rather than a command. It can be applied with `kubectl`, committed to a git tree, and reconciled by Flux like everything else in the estate, which also makes it scriptable and testable for the first time.

The guarantee changes in kind. Previously a burst was cleaned up if the process survived to clean it up. Now an instance is destroyed by whichever layer notices first, and the layers do not share a failure mode: the controller reconciling, the garbage collector diffing, and the deadline carried on the resource itself.

The cost is a controller to operate, with leader election, RBAC and a deployment, where previously there was a binary on a laptop. That is a real increase in moving parts and is accepted because the guarantee is the point of the tool.

The bedrock CronJob and its RBAC are removed, and the race it carried goes with them. bedrock's README claim that horizon deletes the server and its `Node` object together becomes true rather than aspirational.

The concrete parameters: the orphan collector sweeps once a minute, deletes an instance whose owning lease is gone once five minutes past the deadline recorded on its own label, and deletes it immediately if that label cannot be parsed at all. An instance that has not launched within five minutes, or has launched but whose node has not registered within fifteen, is released and its lease marked degraded rather than left waiting indefinitely. The schema commits to bounds independently of any of this machinery: `replicas` up to 8, `duration` up to 8h, `teardownGrace` up to 15m, all rejected by the API server rather than the controller. Node deletion carries its own guard in the other direction: the controller refuses to delete a `Node` whose `horizon.dev/lease-uid` label does not match the lease doing the deleting, and sends the Node UID as a delete precondition, so a node recreated under the same name during a race is never removed by mistake.

This record supersedes the burst orchestration in [0015](0015-standalone-burst-scaler-credential-model.md). The credential model from that record stands. It also supersedes [0007](0007-on-demand-pools-via-machinedeployments.md): the two-mode capacity idea from that record carries forward, but the MachineDeployment mechanism it described is now a lease. It supersedes [0008](0008-retire-watch-daemon-and-wireguard.md), whose explicit-actions, read-only tool is now the long-running controller itself. And it supersedes [0013](0013-cluster-api-over-terraform.md), whose scale, burst, drain, and status paths against Cluster API objects no longer exist in that form.
