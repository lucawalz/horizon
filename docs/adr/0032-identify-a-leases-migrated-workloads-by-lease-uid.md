---
status: accepted
date: 2026-08-25
---

# 0032. Identify a lease's migrated workloads by lease UID

## Context

`savedPlacementMarkers` in `internal/k8s/migrate.go` stamps a migrated Deployment or StatefulSet with two markers on its own metadata: the annotation `horizon.dev/pre-burst-placement`, holding the workload's original placement as JSON, and the label `horizon.dev/burst-placement: "true"`. Neither records which lease moved it. Lease identity reached only the live pod spec, as node affinity keyed on `horizon.dev/lease-uid` and a toleration valued with the lease name, and neither the migrate path nor the restore path read either.

This is the same shape of defect ADR 0031 fixed one layer down, where a node carried the category label `horizon.dev/pool=reserved` and no owner, so a second lease could not tell its own capacity apart. A migrated workload carries a category and no owner, so a second lease cannot tell its own workloads apart.

Three consequences followed. `restoreWorkloads` filtered on annotation presence alone, so one lease's teardown restored another lease's workloads while that lease was still Active, and the other lease never re-migrated because its `WorkloadMigrated` condition was already True. `migrateWorkloads` skipped an already-annotated workload but still reported it, so a lease wrote another lease's workloads into its own `status.migratedWorkloads`. And both eviction paths swept every non-DaemonSet pod in the namespace except those matched by a self-rolling workload the call itself touched, so one lease's migrate and restore evicted another lease's running pods.

## Decision

Ownership moves onto the marker. The workload keeps `horizon.dev/burst-placement: "true"` as the category and gains `horizon.dev/lease-uid: <uid>` as the identity, the same key the node side already uses, so one identity concept spans nodes and workloads. The value is the UID rather than the lease name because a name can be reused once a lease ends and a UID cannot, which is the reasoning ADR 0031 already records.

Both verbs then compare owners. Migrate patches and claims a workload with no annotation, claims without patching one it already owns, and neither patches nor claims one owned by another lease. Restore restores a workload this lease owns, and leaves another lease's workload alone.

A marker written before ownership was recorded names no lease, and both verbs treat it as unheld. Migrate stamps the owner onto it with a label only patch and then claims it, leaving the saved placement annotation and the live pod spec untouched, so the invariant that a claimed workload is an owned workload holds for every path. Restore restores it. Nothing is taken from another lease, because an unowned marker by definition names no holder, and the alternative leaves a workload pinned to a lease that can no longer restore it.

The two readiness gates scope differently, and the difference is deliberate. `WorkloadOnBurstNodes` counts only the pods matching the selectors of the workloads this lease owns, because a lease moved its own workloads and nothing else. A pod belonging to another lease, a bare pod with no controlling workload, and a Job pod would each otherwise hold that gate false for as long as the lease lives. `WorkloadOffBurstNodes` stays node scoped and asks only whether any non-DaemonSet pod still sits on one of this lease's nodes, because restore clears the owner marker before that gate runs, so the node a pod occupies is the only remaining evidence that it still holds capacity about to be deleted.

Eviction inverts from a skip list to a target list: each path evicts the pods of the workloads that the call itself patched or restored and that do not roll themselves. That states the actual intent rather than approximating it, and it confines both paths to the lease's own workloads.

`ClassifyMigratability` gains the lease identity and a reason naming a workload held by another lease, so the conflict reaches `status.migrationWarnings` before a move is attempted rather than leaving a lease waiting on a readiness gate it can never satisfy.

The asymmetry between the two verbs is deliberate and is the fail-safe direction for each. Migrate must never claim what it cannot prove it owns, because the cost is a false success. Restore must never abandon a workload no lease can claim, because the cost is a workload pinned to a dead lease's affinity, unrecoverable without hand editing, whereas a redundant restore is at worst an extra rollout.

## Options considered

- Record the owning lease inside the placement annotation's JSON. Rejected. Ownership then cannot be answered by a label selector, and it becomes undecidable when the annotation fails to parse, which is an already-handled error case, forcing a choice between abandoning a workload and restoring one that may belong to someone else.
- Replace the `horizon.dev/burst-placement` value `"true"` with the lease UID. Rejected. It collapses the category and the identity into one label and loses the ability to ask whether a workload is on burst capacity at all, which is the same conflation ADR 0031 rejected on the node side.
- Tolerate an unowned marker rather than adopt it, claiming it without writing an owner. Rejected, and adoption was taken instead. The accounting that first favoured tolerating it counted only the cost of the extra metadata write and treated the ambiguous state as unreachable in practice. Both halves were wrong once the readiness gate became ownership scoped. A claimed workload that carries no owner contributes no selector to that gate, so the lease reports its move done, never reaches ready, and waits until it expires; and any other lease's teardown is still free to restore it while this lease holds it, which is the defect this record exists to close. The state is also reachable from any lease that was Active and migrated when the operator rolled forward, not only from a release predating this record.
- Leave the markers alone and have each lease consult the other leases it overlaps with. Rejected. It treats the symptom at teardown rather than fixing the marker both verbs key on, and it needs a lease lister keyed by workload namespace that does not exist.

## Consequences

Two concurrent leases targeting one namespace stay isolated across the whole workload lifecycle, not only in scheduling. Each lease patches, claims, restores and evicts only the workloads it moved.

The eviction inversion also resolves one defect previously carried by the target-set work, that a bare pod with no controlling owner was evicted and nothing recreated it, because a bare pod now matches no patched or restored workload. The Job-pod readiness jam and the PodDisruptionBudget abort mid-namespace are untouched and remain that work's.

The inversion also has a cost, on the teardown side. Restore now evicts only the pods of the workloads it restored that do not roll themselves, while the off-burst gate stays node scoped, so a pod that restore never touched can hold that gate open until the teardown budget runs out. The drain that follows draws on the same budget and does nothing once it reaches zero, which would delete a node with no drain at all, the failure ADR 0021 records. The gate therefore stops waiting once the remaining budget falls to a reserved share of the teardown grace, and the drain always gets that share. Half is the share chosen: it scales with whatever grace a lease asks for rather than fixing a floor that a short grace could never clear, and it splits a budget both steps have an equal claim on.

Both verbs and both gates now refuse a workload whose selector names no labels, rather than reading it as matching every pod or no pod at all. A Deployment or StatefulSet without a selector cannot exist at the API server, so the refusal costs nothing in the cluster and removes a silent wrong answer from every path that keys on selectors.

`RestorePlacement` and `ClassifyMigratability` gain a lease identity parameter, matching `Migrate`.
