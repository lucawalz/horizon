---
status: accepted
date: 2026-04-25
---

# 0001. Drive bedrock's Terraform module instead of owning cloud IaC

## Context

A burst needs a real cloud node: a Hetzner VM, provisioned from a NixOS image, joined to the cluster over WireGuard. That infrastructure is already declared in the companion bedrock repository, where the rest of the homelab estate lives. horizon is the runtime controller that decides when to add and remove capacity. The question is whether horizon should carry its own copy of the cloud definitions or reach into bedrock's.

## Decision

horizon drives bedrock's Terraform module rather than redefining it. The `infra_path` setting in the config points at the module directory, which `Load` resolves to an absolute path and stats so a missing path fails early. The Hetzner provider shells out to that module through terraform-exec: it runs init, selects a per-burst workspace, applies with the generated variables, and reads the server outputs back. horizon owns no `.tf` files of its own.

## Options considered

- Own the cloud IaC inside horizon. Self-contained, but it duplicates bedrock's module and image, so every infrastructure change has to be made and kept in sync in two places.
- Drive bedrock's module through `infra_path`, chosen. One source of truth for infrastructure stays in bedrock, and horizon stays a stateless controller over it.

## Consequences

There is one definition of a burst node, in bedrock, and horizon cannot drift from it. The cost is a hard dependency: horizon needs a local bedrock checkout and the Terraform CLI present, and a valid `infra_path`, or it refuses to start. The repository boundary stays clean: bedrock declares what a node is, horizon decides when one exists.
