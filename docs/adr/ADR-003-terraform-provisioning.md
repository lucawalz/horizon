# ADR-003: Terraform for VM Provisioning

**Status**: Accepted
**Date**: 2026-04-30

## Context

Cloud VMs must be provisioned and destroyed reliably across at least two providers (Hetzner, AWS). Direct cloud SDKs require hand-rolling state management, idempotency, and plan/apply semantics for every resource type.

## Decision

Use Terraform (via `terraform-exec`) for VM provisioning. Terraform modules live in the `bedrock` repository at a path configured by `infra_path` in `config.yaml`.

## Consequences

- Terraform's state file and plan/apply lifecycle handle idempotency and rollback without custom logic in horizon.
- The same Provider interface works for any cloud with a Terraform provider, lowering the effort to add AWS or IBM Cloud.
- `terraform-exec` wraps the Terraform binary, so Terraform must be installed on the operator's machine.
- Secrets are passed via environment variables; no credentials appear in state files or CLI arguments.
