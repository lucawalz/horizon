# ADR-006: Two-Repository Split

**Status**: Accepted
**Date**: 2026-04-30

## Context

The project requires both application code (Go CLI) and infrastructure files (Terraform modules, NixOS configurations, Flux manifests). Keeping everything in one repository couples CLI releases to infrastructure changes and makes the Git history harder to navigate.

## Decision

Maintain two repositories: `horizon` (Go CLI, this repo) and `nixos-homelab` (Terraform modules, NixOS configurations, Flux manifests). horizon references the homelab repository at runtime via a configurable `infra_path` in `config.yaml`.

## Consequences

- CLI and infrastructure have independent release cadences and review scopes.
- The homelab repository is also the source of truth for the live cluster, so infrastructure changes are applied by Flux and auditable independently of CLI changes.
- Cross-cutting changes (e.g., a new Terraform variable that also requires a new CLI flag) require coordinated commits in both repositories.
