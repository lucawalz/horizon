---
status: accepted
date: 2026-08-25
---

# 0031. Identify a lease's nodes by lease UID

## Context

Every rented node carried the same `horizon.dev/pool=reserved` label, so a second concurrent lease could not tell its own nodes apart from another lease's. The workload affinity horizon writes at migration keyed on that label, the injected toleration used `Operator: Exists` with no value, and the readiness gates counted any node carrying the label as the lease's own. Two leases running at once ended up sharing capacity: a workload could schedule onto, or tolerate the taint of, a node it was never granted.

The pool label cannot be retired to fix this. It answers a question other readers depend on: is this a burst node at all. The node-join contract labels a node with it before anything else runs, and the manager's Node-cache filter in `internal/manager/manager.go` (`cacheOptions()`) selects on it, so every controller that watches nodes, the orphan sweeper in `internal/controller/orphan_controller.go` included, only ever sees burst capacity to begin with. It also has to stay a fixed constant rather than grow a per-lease suffix or value, because `internal/cli/cloudinit.go` validates the exact assignment `horizon.dev/pool=reserved` against the rendered cloud-init, and the node image bakes that same literal in at build time. Changing its value per lease would mean changing the image.

What the pool label cannot do is answer whose node. It was never meant to; it identifies a category, not an owner.

## Decision

Ownership moves to `horizon.dev/lease-uid`, a label already written on each node at adoption, one that carries a value unique to the lease that holds it. Node affinity keys on this label instead of the pool label, so a migrated workload can only schedule onto a node its own lease acquired.

The burst taint keeps its existing value, the lease name, and the injected toleration now matches it with `Operator: Equal` instead of `Operator: Exists`. Node affinity uses the UID and the toleration uses the name because each already carries the value that fits it: the taint has always been valued with the lease name, so the toleration mirrors that value rather than introducing a second identifier, while the UID is what the affinity needs because a lease name can be reused once a lease ends and a UID cannot.

The readiness gates that decide whether a workload has landed on, or left, burst capacity now select nodes by the same `horizon.dev/lease-uid` label rather than by the pool label, so a lease reports itself ready only once its workload sits on nodes it actually holds.

## Options considered

- Suffix or template the pool label's value per lease. Rejected. It would break the fixed assignment `internal/cli/cloudinit.go` validates and the node image bakes in, which both need `horizon.dev/pool=reserved` to be a single constant, not a namespaced value.
- Key everything, including the toleration, on the lease UID. Rejected. The burst taint already carries the lease name as its value, so valuing the toleration with the UID would mean writing a second identifier onto every taint and toleration pair for no gain.
- Leave the pool label as the sole ownership signal and add a second admission check elsewhere. Rejected. It treats the symptom at the point workloads land rather than fixing the label that scheduling itself keys on, and it leaves the toleration still tolerating any lease's taint.

## Consequences

Two leases can run concurrently and stay isolated: each lease's workload can schedule only onto, and only tolerates the taint of, the nodes it was granted. No node-side or image change was required, because the lease-uid label was already written at adoption and the pool label's contract with the join process and the manager's Node-cache filter is unchanged.

`poolNodeAffinity` and `poolNodePresent`, which built and checked affinity against the shared pool label, are deleted along with the toleration's old value-blind `Exists` operator.
