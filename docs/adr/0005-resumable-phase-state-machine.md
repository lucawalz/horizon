---
status: accepted
date: 2026-05-02
---

# 0005. Record burst progress as a resumable phase state machine

## Context

A burst is a multi-step, long-running operation: back up the namespace, apply Terraform to create a VM, register it as a WireGuard peer on the hub, wait for it to register as a K3s node, then migrate the workload. Each step takes time and several touch cloud resources that cost money. A crash, a signal, or a network failure partway through must not leave a half-built burst that nobody can see and nothing cleans up: an orphaned Hetzner VM keeps billing whether or not the cluster knows it exists.

## Decision

The burst runs through a sequential step runner where each step carries its own rollback. If a step fails, the runner unwinds the completed steps in reverse, calling each rollback, so a failed apply is destroyed and a failed migration is reverted. Progress is recorded as a named phase, Idle, BackingUp, Provisioning, Joining, Migrating, Running, then TearingDown on unwind, in the `horizon-state` ConfigMap in `kube-system`, updated as each step begins. Because the phase lives in the cluster, an interrupted run is inspectable after the fact rather than lost with the process.

## Options considered

- Fire-and-forget: run the steps and exit. Simple, but a crash mid-run orphans cloud resources and leaves no record of how far the burst got.
- A persisted phase machine with per-step rollback, chosen. The runner unwinds failures itself, and the in-cluster phase makes a partial run visible.

## Consequences

A failed burst cleans up after itself, and an interrupted one can be read from the ConfigMap and torn down deliberately. The cost is discipline: every step has to define a correct rollback, and the phase write is best-effort, so the cluster's recorded phase can lag the true state if a write fails. Per-node details are kept in local state files alongside the cluster-level phase.
