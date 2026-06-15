---
status: accepted
date: 2026-06-15
---

# 0006. Become a thin Cluster-API operator instead of a provisioning controller

## Context

horizon began as a laptop controller that owned the whole provisioning path: it drove bedrock's Terraform module to create a Hetzner VM, registered that VM as a WireGuard peer on the home hub over SSH, waited for it to register as a K3s node, and moved the workload onto it. That path put cloud provisioning, overlay networking, and node lifecycle inside a single command-line tool. Meanwhile bedrock adopted Cluster API as its substrate: the Cluster API Provider Hetzner (CAPH) for infrastructure and cluster-api-k3s for bootstrap and control planes, managed by Rancher Turtles, with Tailscale for connectivity. With that substrate in place the provisioning logic in horizon duplicated work the cluster now does declaratively.

## Decision

horizon becomes a thin operator over Cluster API objects rather than a provisioner. It no longer shells out to Terraform, manages WireGuard peers, or owns a provider abstraction. Instead it reads and writes Cluster API resources (MachineDeployments and Clusters) through a controller-runtime client against a target kubeconfig context. The substrate that defines what a node is, CAPH plus cluster-api-k3s under Rancher Turtles, and the connectivity that reaches it, Tailscale, both live in bedrock. horizon decides when pools scale and when clusters exist, and leaves the definition of infrastructure to bedrock's CAPI providers.

## Options considered

- Keep the Terraform-and-WireGuard provisioning path. It works, but it duplicates the lifecycle that CAPH and cluster-api-k3s already own declaratively, and it keeps SSH peer management and a per-burst Terraform workspace inside a laptop tool.
- Operate over Cluster API objects, chosen. The substrate owns provisioning and connectivity, and horizon shrinks to scaling pools and managing clusters through the Kubernetes API.

## Consequences

horizon owns no cloud credentials, no Terraform state, and no overlay configuration. The dependency on a local bedrock checkout and the Terraform CLI is gone, replaced by a reachable cluster and a kubeconfig context. The cost is that horizon now requires the full CAPI substrate to be present in bedrock before it can do anything: the providers, the templates, and Rancher Turtles. This record supersedes [0001](0001-drive-bedrock-terraform.md) (driving bedrock's Terraform module) and [0002](0002-pluggable-provider-interface.md) (the provider interface), both of which describe the retired provisioning path. The infrastructure side of this substrate is recorded in the [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.
