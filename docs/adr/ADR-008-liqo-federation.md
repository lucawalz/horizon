# ADR-008: Liqo for Multi-cluster Federation (Mode 3)

**Status**: Accepted
**Date**: 2026-04-30

## Context

Mode 3 requires workloads to span the homelab cluster and a remote cloud cluster transparently, without application-level changes. A federation layer must work with K3s specifically, as K3s omits several components present in full Kubernetes distributions.

## Decision

Use Liqo for multi-cluster federation in Mode 3. Liqo compatibility with K3s has been verified before committing to this approach. Modes 1 and 2 (single cloud node join) do not use Liqo.

## Consequences

- Liqo's virtual-node abstraction lets the standard Kubernetes scheduler place pods across clusters using unmodified workload manifests.
- `liqoctl unpeer` is called before `terraform destroy` during teardown to avoid leaving a ghost virtual node in a `NotReady` state.
- Mode 3 adds operational complexity and a longer setup sequence compared to Mode 1/2; it is scoped to the later phase of the project.
