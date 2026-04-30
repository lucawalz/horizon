# ADR-002: Velero for Workload Backup and Restore

**Status**: Accepted
**Date**: 2026-04-30

## Context

The burst workflow must capture workload state before migrating pods to a cloud node, so the cluster can be restored if provisioning fails mid-run. Building custom backup logic for Kubernetes workloads — including PVC snapshots, CRD capture, and provider-specific storage integration — is a significant engineering investment with its own failure modes.

## Decision

Use Velero for backup and restore. horizon triggers Velero Backup CRs via `client-go` and polls until `phase == Completed && errors == 0` before proceeding.

## Consequences

- Velero handles provider-agnostic object storage, CSI snapshot integration, and restore ordering.
- Triggering via the Kubernetes API (not the Velero CLI) keeps the integration testable with standard mocks.
- A backup with `errors > 0` causes horizon to abort rather than proceed with a potentially incomplete snapshot.
- Velero must be installed and configured on the cluster before `horizon burst` can run.
