# ADR-007: Hetzner as Primary Cloud Provider

**Status**: Accepted
**Date**: 2026-04-30

## Context

The project demonstrates multi-cloud burst capability with at least two providers. Development velocity and demo reliability depend on fast VM startup times and a low-friction API. A European provider is preferred to minimize latency to the homelab.

## Decision

Hetzner Cloud is the primary provider used for development, testing, and the main demo. AWS is the secondary provider. IBM Cloud is deferred as a low-priority extension.

## Consequences

- Hetzner VM startup (under 30 s) keeps the E2E burst cycle short during development and demos.
- Hetzner's flat-rate pricing avoids unexpected costs during iterative testing.
- The `Provider` interface (`internal/provider`) ensures AWS uses the same code path with a different backend, so switching providers in `config.yaml` requires no code changes.
