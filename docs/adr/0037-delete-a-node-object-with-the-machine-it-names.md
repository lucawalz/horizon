---
status: accepted
date: 2026-08-26
---

# 0037. Delete a node object with the machine it names

## Context

[0021](0021-node-side-dead-mans-switch-on-two-clocks.md) moved the teardown guarantee onto the machine itself. Two node-lifecycle gaps sit directly beside it, both measured against the live cluster and real cloud machines on 2026-08-26. Each leaves a `Node` object standing for a machine that no longer exists, and each does a different kind of damage.

The first gap accumulates. Horizon deletes the node during release, and the machine's kubelet re-registers it before the machine powers off. The re-registered object carries only what cloud-init gives the kubelet, `horizon.dev/pool=reserved`, and never `horizon.dev/lease-uid`, which horizon writes at adoption. The orphan sweeper returned immediately for any node without that label, so a re-registered object was permanently invisible to it. One survived 52 minutes and every teardown that followed, `NotReady`, with a creation timestamp recording the release moment rather than the join. The manager's node cache selects on the pool label, per [0031](0031-identify-a-leases-nodes-by-lease-uid.md), so these objects stay in memory as well as in etcd.

The second gap blocks a live lease. A 60 minute lease held a joined node and an armed watchdog when the operator was scaled to zero. The machine destroyed itself at its deadline, which is [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) working as designed. On restart the operator confirmed the instance absent, marked its entry released and provisioned a replacement within a second. It then left the dead machine's node object standing, matched the replacement to it by name, recorded the instance as joined and waited on a kubelet that no longer existed. Every condition on that node read `Unknown` and the address it reported belonged to the dead machine. The lease sat in `Provisioning` for over twelve minutes and had to be released by hand.

The sweeper cannot close the second gap. That node carries a lease uid, its lease is live, so the stranded test correctly answers that it is not stranded.

## Decision

Delete a node object as soon as the machine it names is known to be gone, on every path that reaches that conclusion, and judge an unadopted node by the same stranded test as an adopted one.

The orphan sweeper no longer requires the adoption label to consider a node at all. A labelled node is still protected by its own lease, because a lease that still holds the uid answers the question before any provider is asked. An unlabelled node has no lease to consult, so absence at every configured provider decides alone. The readiness guard is unchanged, and it is what makes this safe for a node that has registered and has not yet been adopted: such a node is not ready either, but its name matches an instance a live lease still holds, so absence is never proven and the node is never deleted. Absence has to hold at every configured provider, and a sweeper that can build none proves nothing, so both of those failures retain the node rather than remove it.

The lease loop closes its own gap. Confirming an instance absent at the provider was the one path that concluded a machine was gone without removing the node it had recorded. It now does what the release path has always done, and deletes the node the entry names under the same ownership guard: horizon refuses to delete a node that does not carry the lease uid it expects.

Replacing the destroyed machine is kept rather than suppressed. The lease is a promise of a number of machines for a duration, and 56 minutes of that duration remained. The controller already separates the two cases it needs to: an instance retired for failing to launch or to register records the failure on its entry, and an entry carrying a failure is not a slot to refill, so the lease degrades instead of looping, while an instance that simply vanished records no failure and its slot refills. Making the refill depend on guessing that the watchdog rather than something else destroyed the machine would silently shrink a lease below its replica count with nothing reporting it, which is the failure mode [0017](0017-capacity-lease-controller-over-cli-saga.md) and [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) both push away from. The replacement latches its own backstop, per the update on [0021](0021-node-side-dead-mans-switch-on-two-clocks.md), and `status.expiresAt` is derived from the instances the lease holds rather than stamped once, so a replaced machine cannot extend the lease.

## Options considered

- Decide staleness from the node's `spec.providerID`, deleting a node whose identifier names a machine other than the one the lease now holds. Rejected on measurement. K3s stamps `k3s://<node-name>` on every node, confirmed against the cluster, and horizon never passes a cloud provider identifier to the kubelet, so the value names the node and not the machine. It reads the same on a dead machine and on its replacement, which is exactly the pair it would have to separate, and a rule that deleted a node whose identifier did not match an instance would fire on every healthy node instead. `matchesProviderID` keeps its place in node matching, where a provider-scoped identifier costs nothing on a node that happens to carry one, but it cannot carry this decision.
- Compare the node's creation timestamp against the instance's. Rejected. It reads the API server's clock against the provider's, which the controller already treats as untrusted relative to each other, and it infers from two timestamps what the provider can be asked directly.
- Leave the stale node and let the replacement's kubelet take it over. It does not take it over. The replacement never registered in the twelve minutes it was watched, and k3s keeps a node password secret per node name, `<name>.node-password.k3s` in `kube-system`, verified present on the cluster, against which a rejoining agent is validated. A new machine claiming a used name is refused for as long as that secret and the node object it belongs to survive.
- Hand the second case to the orphan sweeper as well. Rejected. The node is not stranded there: its lease is live and its slot holds a machine. Deciding which machine a live lease's node belongs to means rebuilding the lease loop's own record of it inside a controller that deliberately knows nothing about lease internals.
- Stop replacing a machine the watchdog destroyed. Rejected, for the reasons recorded above.

## Evidence

Both defects come from a live run against the cluster and real cloud machines on 2026-08-26, not from reading the code. The first was a released lease whose node object outlived it by 52 minutes with the pool label and no lease uid. The second was a watchdog destruction with the operator scaled to zero, after which the lease provisioned a replacement and then waited on the dead machine's node object until it was released by hand.

Both are held by regression tests. An unadopted node whose instance is gone is now swept; an unadopted node whose instance still exists is retained, which is the guard that protects a node in the middle of joining; and a ready node with no lease and no instance is still retained, which is the guard that keeps that first test from becoming a licence to delete anything unlabelled. For the lease loop, an instance destroyed behind the controller's back loses its node before the replacement is created, and a fresh node registering under the same name brings the lease to ready.

## Consequences

A node carrying the pool label with no adoption label is deleted once it is not ready and no configured provider holds an instance of its name. That includes a node hand-labelled into the pool. The pool label is horizon's contract with the join process, so anything wearing it claims to be burst capacity, and burst capacity with no machine behind it is what the sweeper exists to remove.

The sweeper now asks the providers about unadopted nodes, one lookup per configured provider per minute for as long as such a node is not ready. A ready node is still answered without consulting any provider at all.

If the operator is down for the whole life of a machine, no lease ever adopts its node or records its name, so the lease loop has no name to delete when the machine goes. The sweeper covers exactly that node once the instance is gone. The two halves of this decision compose, and neither alone is sufficient.

Deleting the node along with the machine also collects the pods bound to it, instead of leaving them to a kubelet that is not coming back.
