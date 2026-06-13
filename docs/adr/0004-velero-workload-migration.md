---
status: accepted
date: 2026-05-02
---

# 0004. Migrate a workload with a backup, an affinity rewrite, and an eviction

## Context

A burst is only useful if the heavy workload actually lands on the new cloud node. horizon has to move a running namespace from the local nodes onto the burst node it just provisioned. True live migration of arbitrary Kubernetes workloads, with their volumes and in-flight state, is hard and not something a small controller should reimplement. The cluster already runs Velero for backups, and the Kubernetes scheduler already knows how to place pods.

## Decision

Migration reuses both. First horizon takes a Velero backup scoped to the target namespace, the same namespace-scoped backup the cluster runs for disaster recovery. Then it labels the burst node, rewrites required node affinity onto every deployment and statefulset in the namespace to point at that label, saving each original affinity first, and evicts the namespace's pods, skipping daemonset pods. The scheduler reschedules the evicted pods, and the affinity sends them to the burst node. Rolling back restores the saved affinity, removes the node label, and evicts again so the pods return.

## Options considered

- Live migration of running pods. It would preserve in-flight state, but it is complex and does not generalize to arbitrary workloads, well beyond what this controller should own.
- Backup, affinity rewrite, and eviction, chosen. It composes a real backup tool with the scheduler, and the namespace backup doubles as both a safety net and the restore primitive.

## Consequences

The move is built from tools the cluster already trusts, and a Velero backup exists before any pod is disturbed. The cost is that this is a reschedule, not a live handoff: evicted pods restart on the burst node and serve no requests in between, so the workload absorbs a short interruption. The Velero side of this, the namespace-scoped backups in bedrock, is recorded in that repository's [0009](https://github.com/lucawalz/bedrock/tree/main/docs/adr).
