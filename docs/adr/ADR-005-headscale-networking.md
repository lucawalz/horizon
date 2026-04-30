# ADR-005: Headscale for Overlay Networking

**Status**: Accepted
**Date**: 2026-04-30

## Context

Cloud burst nodes need bidirectional connectivity to the homelab cluster across NAT and without opening public firewall ports. Using the managed Tailscale SaaS control plane exposes node coordination metadata — hostnames, IP assignments, pre-auth key lifecycle — to an external service.

## Decision

Use Tailscale (WireGuard data plane) coordinated by a self-hosted Headscale instance running on the homelab cluster. Ephemeral pre-auth keys are generated per burst via the Headscale API and revoked immediately after first use.

## Consequences

- Full control over node enrollment, ACL policies, and key lifecycle without any external SaaS dependency.
- Pre-auth keys expire after first use, limiting the blast radius if a key is intercepted during provisioning.
- horizon manages the Headscale API directly (`internal/headscale`) to create pre-auth keys and remove nodes on teardown.
- WireGuard (`wg0`) remains a documented fallback if Headscale is unavailable.
