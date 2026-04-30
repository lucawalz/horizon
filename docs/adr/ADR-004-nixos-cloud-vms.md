# ADR-004: NixOS for Cloud VMs

**Status**: Accepted
**Date**: 2026-04-30

## Context

Cloud burst nodes must join the homelab K3s cluster quickly and reliably. Configuration drift between provisioning runs — caused by mutable cloud-init scripts or imperative setup steps — produces hard-to-reproduce failures during demos and testing.

## Decision

Cloud VMs run NixOS with a declarative configuration that includes the K3s agent, Tailscale, and firewall rules. Deployment uses `nixos-anywhere` for initial installation over SSH.

## Consequences

- The same Nix expression always produces the same system state; there is no configuration drift between runs.
- Topology changes (e.g., switching K3s channel, adding kernel modules) are a one-line diff rather than an imperative runbook.
- `nixos-anywhere` installs NixOS from a plain Hetzner cloud image without requiring a pre-built snapshot, keeping the Terraform module simple.
- Initial provisioning takes longer than a pre-baked image because NixOS builds the system closure at install time.
