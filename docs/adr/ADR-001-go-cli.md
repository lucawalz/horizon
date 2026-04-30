# ADR-001: Go for the CLI

**Status**: Accepted
**Date**: 2026-04-30

## Context

horizon needs a CLI that integrates with the Kubernetes ecosystem, ships as a self-contained binary, and can handle concurrent operations — particularly the watch daemon, which polls Prometheus continuously and reacts to pressure changes alongside ongoing burst activity.

## Decision

Implement horizon in Go.

## Consequences

- `client-go` and the full Kubernetes ecosystem are first-class citizens.
- Goroutines are idiomatic for the watch loop and concurrent step execution in the runner.
- A single, statically-linked binary simplifies distribution and installation.
- `cobra` and `viper` provide a conventional CLI and config structure that the ecosystem already understands.
