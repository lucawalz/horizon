---
status: superseded by 0006
date: 2026-04-25
---

# 0002. Put cloud calls behind a provider interface

## Context

Every cloud-specific action a burst needs, applying Terraform, destroying it, reading the node's address, generating its variables, talks to one backend: Hetzner Cloud. The orchestration around it, the runner steps, the migration, the phase machine, is cloud-agnostic. The risk is letting Hetzner calls leak into that orchestration so that a second backend, if it ever arrives, means rewriting the burst logic rather than adding an implementation.

## Decision

A small `Provider` interface defines the seam: `Apply`, `Destroy`, `Status`, and `GenerateTFVars`. The Hetzner package implements it and is the only implementation. The burst runner depends on the interface, not on Hetzner directly, so the cloud-specific code stays behind four methods.

## Options considered

- Hardcode Hetzner throughout the orchestration. Simpler to write today, but it scatters provider assumptions across the runner and would force a rewrite to add any other backend.
- A provider interface with one implementation, chosen. The seam is four methods wide, so it costs almost nothing now while keeping the orchestration free of Hetzner specifics.

## Consequences

The orchestration logic reads against a stable contract and stays testable with a fake provider. Adding a second cloud is implementing the interface, not editing the burst flow. The honest caveat is YAGNI: with a single implementation the interface is an unproven abstraction, and Hetzner-only concepts like the WireGuard keypair and Terraform workspaces still live in the concrete type, so a real second backend may yet reshape the contract.
