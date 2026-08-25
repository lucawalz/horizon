---
status: accepted
date: 2026-08-25
---

# 0035. Replicate a workload as a lease-owned copy

## Context

A lease with `spec.workload` moved every matched Deployment and StatefulSet onto the leased
nodes and put it back at expiry. That is the right shape for capacity a workload has to be
carried onto, such as a batch run that needs a bigger machine than the cluster owns. It is
the wrong shape for a workload that has to keep serving from where it already runs and only
wants more of itself for a few hours.

Moving is also disruptive by construction. Burst nodes are tainted, so a pod reaches one
only if its template carries the toleration, and editing `spec.template` mints a new
ReplicaSet and rolls every replica. [0032](0032-identify-a-leases-migrated-workloads-by-lease-uid.md)
and the migratability classifier exist because that rollout is safe only for a workload with
`RollingUpdate` and real surge capacity, and many are not that shape. A lease that only
wants extra replicas pays the whole cost of a move to get them.

## Decision

`spec.workload.mode` selects between `move`, which is the default and is unchanged, and
`replicate`. In replicate mode the lease creates a copy of each matched Deployment in the
same namespace, pinned to the lease's own nodes and running `spec.workload.burstReplicas`
pods, and deletes the copy at teardown. The matched workload is never written to on any
path.

The mode is latched into `status.placedWorkloadMode` before the first placement and every
teardown step dispatches on that latch rather than on `spec.workload.mode`. The spec is
mutable while a lease lives, and the two modes undo themselves in opposite ways, so a mode
read back at teardown is a mode that can have changed since the placement it is supposed to
reverse. A lease that moved a workload and then reads `replicate` deletes copies that do not
exist and never restores the placement it saved, leaving the original pinned by a required
node affinity to a label no node carries once the lease is gone, which is a permanent outage
of a workload the lease was only borrowing capacity for. The mirror case leaves a copy
running on capacity that is about to be returned. This is the same latching that
[0032](0032-identify-a-leases-migrated-workloads-by-lease-uid.md) applies to ownership and
0034 applies to the namespaces teardown reads out of `status.migratedWorkloads`: what the
lease did is recorded where the lease did it.

`spec.workload.mode` is immutable as well, so the edit is refused rather than merely
survived. The latch is still what teardown reads, because a rule comparing the field against
its old value cannot see a `spec.workload` that appears mid-lease, and because the record of
what a lease did belongs in its status either way.

The field is named `burstReplicas` rather than `replicas` because `spec.replicas` already
exists one level up and counts machines. Two quantities under one name in one object is how
someone rents eight servers to run two pods, so both fields now carry a description in the
CRD as well.

The copy is named `<original>-burst-<hash8>`, the hash taken from the lease UID and the
original's name together. That is deterministic, so a repeated pass finds the copy it already
made rather than making another; it is distinct per lease, so two leases replicating one
workload do not collide; and it is distinct per workload, so the trim that fits the name into
the 253 character object name limit cannot fold two long-named originals of one lease onto a
single copy. Hashing the lease UID alone did exactly that, and the second create came back
`AlreadyExists`, which the repeated pass has to tolerate, so one workload would have been
reported as replicated while running no burst pods at all.

The copy carries the `horizon.dev/lease-uid` label, which is the ownership convention
[0031](0031-identify-a-leases-nodes-by-lease-uid.md) and 0032 established and which every
existing helper keys off, and an owner reference to the `CapacityLease`. A namespaced object
may name a cluster-scoped owner and garbage collection honours it. The owner reference is a
backstop rather than the mechanism, because collection fires when the lease object is
deleted and a lease usually expires long before that, so teardown deletes the copy itself and
gates the node release on its pods being gone. The controller writes workloads through a
`kubernetes.Interface` rather than a scheme-aware client, so the reference is built by hand.

The copy's pods carry the original's labels plus `horizon.dev/burst-copy`, and the copy's
selector is the original's plus the same label. A pod matches a selector when its labels are
a superset of it, so a Service that selects the original reaches the burst replicas without
being told about them, which is the point of the mode. The extra label in the copy's selector
is what keeps the two ReplicaSets from contending over one set of pods, so the selector is
built and compared against the original's before anything is created, and a workload whose
selector already carries this lease's burst-copy label is skipped rather than copied into a
pair that names one set of pods twice. Nothing else establishes that invariant: the guard
against copying a copy reads the Deployment's own labels, which say nothing about its
selector.

The copy's pod spec is the original's with four changes, and each one is scoped as narrowly
as it can be. The node affinity is replaced by the lease's own and the node selector is
cleared, because both would otherwise keep the copy off the very nodes it exists to run on.
The rest of the affinity is kept, so a workload that demands one pod per host still gets one
pod per rented host rather than every burst replica packed onto a single node. The burst
toleration is added. The priority class is dropped, because the copy is the expendable half
of the pair and an inherited priority would let it preempt pods already running on the nodes
the lease rents.

Replicate mode reports its own condition, `WorkloadReplicable`, and emits no
`WorkloadMigratable` verdict. Nothing is rolled, so none of the classifier's reasons apply to
a copy. The condition is True once copies exist, and False with `NoMatchingWorkloads` when
the target set names nothing or with `EveryWorkloadSkipped` when every matched workload was
skipped, because renting machines for a typo or for a namespace nothing in it can be copied
is worth reporting loudly rather than sitting Active beside idle capacity.

Four shapes are skipped rather than copied, each named in `status.migrationWarnings` with
its reason while its neighbours are replicated as usual. The line between a skip and a
warning is where the damage lands: a skip is for a shape where the copy harms the original,
and a warning is for a shape where the cost falls on the copy or on an accounting an operator
can accept.

A workload a HorizontalPodAutoscaler targets is skipped. The autoscaler reads pod metrics
through the scale subresource's selector, which is the same subset match, so it sees the
copy's pods, concludes the workload is over-provisioned and scales the original down, which
is the outage this mode exists to avoid. The skip creates nothing, because the copy is what
does the damage, and the reason names move mode as the way to burst that workload: move mode
changes no replica count and so does not fight an autoscaler.

A workload carrying a `DoNotSchedule` topology spread constraint is skipped. The scheduler
counts every pod in the namespace whose labels match the constraint's selector, and the
copy's pods carry the original's labels by design, so the burst replicas count into the
original's own domains and skew them. The original's next pod, whether from a rollout, a
scale-up or a replacement after a drain, can then be refused a node and sit Pending for the
life of the lease. That is the same shape of damage as the autoscaler case, landing on the
original rather than on the copy, so it is treated the same way and is skipped rather than
warned about. A constraint set to `ScheduleAnyway` is left alone: it only scores nodes, so it
costs the original a preference and never a pod. Stripping the constraint from the copy was
rejected as a fix, because the copy's pods still carry the labels the original's own
constraint selects, so the original's domains are skewed whether or not the copy repeats the
constraint.

A StatefulSet is skipped. A copy of one mints a fresh set of PersistentVolumeClaims, which
costs money and leaves cleanup nobody asked for, and the data the workload holds is not in
the copy anyway. Move mode continues to handle StatefulSets exactly as before.

A PodDisruptionBudget that selects the original also selects the copy's pods, by the same
subset match that makes the shared Service work. That is warned about and not skipped. The
teardown drain already forces past budgets, so it degrades rather than breaks, but the
budget's accounting is wrong for the life of the lease rather than only at teardown.

Both checks read other objects, so both are controller-time rather than CEL, and the
ClusterRole gains read access to `autoscaling/horizontalpodautoscalers` and
`policy/poddisruptionbudgets` alongside `create` and `delete` on deployments.

Move mode now skips any workload carrying `horizon.dev/burst-copy`. `Migrate` lists every
Deployment in a target namespace with no matcher, so without the skip a second lease in move
mode would save a placement onto another lease's copy, stamp its own owner label on it, and
own a restore of an object that is deleted out from under it at the other lease's expiry.

## Options considered

- Raise `spec.replicas` on the original and let the scheduler place the new pods on burst
  capacity. Rejected. A bare replica bump does not roll pods, but new pods reach a tainted
  node only if the template tolerates the taint, and editing the template rolls every
  replica, twice over the life of the lease. A guaranteed split between home and burst
  additionally needs a topology spread over a key the scheduler can see on both sides, and
  home nodes carry no pool label at all, so adopting horizon would first require labelling
  every existing node.
- Isolate the copy behind its own labels so nothing selecting the original reaches it.
  Rejected, and not available either. Superset matching means anything selecting the
  original matches a copy carrying the original's labels plus one, and stripping the
  original's labels from the copy would take the shared Service with it, which is the
  mechanism the mode is built on.
- Reuse `WorkloadMigrated` as the progress condition for replicate mode. Rejected. Nothing
  is migrated, and a condition that reads Migrated=True on a lease that never wrote to a
  workload is a false report in the field an operator reads first.
- Let the owner reference alone clean the copy up. Rejected. Garbage collection fires on
  lease deletion, and a lease that expires keeps its object, so the copy would outlive the
  nodes it is pinned to and sit Pending until someone deleted the lease by hand.
- Warn about an autoscaled workload and copy it anyway. Rejected. Every other warning
  describes a cost the operator can accept, while this one describes the original being
  scaled down under load, and the copy is the thing that causes it.

## Consequences

A lease can add capacity to a serving workload without that workload being rolled, moved or
written to at all. Home capacity is unchanged from before the lease to after it, and the
number of burst pods is the copy's replica count by construction rather than an outcome of
scheduler behaviour.

An existing Service in front of the original load-balances across both sets of pods with no
change to the Service, and so does anything else selecting those labels: a NetworkPolicy, a
ServiceMonitor, a PodDisruptionBudget, a topology spread constraint. The first two are the
intent. The third is the cost, and it is reported. The fourth is the one that reaches back
into scheduling the original, and it is the reason such a workload is not copied at all.

`status.migratedWorkloads` names the copies a replicate-mode lease has to delete, in the same
`namespace/kind/name` form and with the same growth rule 0034 gave it. Teardown reads its
namespaces out of that list, deletes every copy the two ownership labels select in each of
them, and then confirms by name that each copy the list records is gone before the list is
cleared. The delete is label scoped so that it can never reach a workload the lease only
moved, and that same scoping means a copy stripped of either label falls out of it silently,
after which the list is the only record that the copy ever existed. A copy that outlives the
delete degrades the lease with `BurstCopyDeleteFailed` and keeps the record, rather than
clearing the list and leaving the copy to run on nodes that are about to be deleted.

A lease name is bounded at 63 characters by the CRD. The name is a label value on the copy,
on the copy's pods and in the copy's selector, and it is the value of the burst taint on
every leased node, so a longer name is refused by the apiserver on the create that carries
it. Bounding the name rather than deriving the copy label from the UID keeps one bound over
every place the name is carried, including the node taint that both modes write.

`status.migrationWarnings` carries skips as well as warnings in replicate mode. The web
interface has wording for the move-mode reasons only, so a replicate-mode reason renders
through the fallback the bundle already carries for a reason it does not know, and the panel
still describes a move. Giving the interface its own replicate wording is not part of this
record.

An autoscaled workload in a namespace targeted in replicate mode is not replicated at all,
and an operator who wants that workload on burst capacity has to say `move`. The reason
recorded against the workload says so rather than only reporting the refusal.
