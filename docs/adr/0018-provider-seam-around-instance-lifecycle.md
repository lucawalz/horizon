---
status: accepted
date: 2026-08-02
---

# 0018. Redesign the provider seam around instance lifecycle and capabilities

## Context

[0016](0016-cluster-agnostic-tool-with-provider-seam.md) drew the provider interface at two methods, list the reserved servers and scale the reserved count to a target, and deliberately rejected a wider one. The reasoning was recorded plainly: image and SSH-key lookup are reached only through scaling, so the interface stays two methods and provider-internal detail stays internal. Under the requirements of the time that was the right call, and the record was correct to reject a plugin framework as speculative.

A lease invalidates the premise. A count cannot be leased, adopted or expired. The controller in [0017](0017-capacity-lease-controller-over-cli-saga.md) needs to create one named instance, ask about that instance, and delete that instance, because ownership and deadlines attach to individual machines rather than to a total. The existing seam also encodes a broken failure model: the scale loop returns a partial count alongside an error and the caller discards it, so a create that fails halfway is invisible.

A second finding makes the widening unavoidable rather than merely convenient. Whether a machine can destroy itself, and what that costs, differs by provider in ways that change the architecture rather than the configuration. Verified against vendor documentation:

Hetzner bills for a server while the server object exists. Powering off does not stop billing, because the reserved capacity is not released until the server is deleted. Self-destruction therefore requires an API call, which requires a delete-capable credential on an ephemeral machine, and Hetzner tokens are project-scoped with no per-resource permissions.

AWS terminates an instance on an in-guest shutdown when `InstanceInitiatedShutdownBehavior` is set to terminate, and billing ends when the state reaches shutting-down. No credential is needed on the machine at all.

Google Compute Engine enforces `maxRunDuration` with `instanceTerminationAction` set to delete, server-side, on ordinary instances. No agent and no credential are needed.

The same requirement therefore costs nothing on one provider, a kernel timer on another, and a security compromise on a third. That is a capability difference, not a cosmetic one, and an interface that cannot express it forces the controller to hard-code provider knowledge it was supposed to abstract.

## Decision

Draw the interface around the lifecycle of an individual instance, and add capability reporting.

The interface creates, gets, lists and deletes instances by name, and reports what the provider can guarantee. A neutral instance type carries the provider identifier, region, state, labels and creation time. Creation takes a request carrying the name, region, size, labels and user data, so labels are applied in the same call that creates the machine as [0017](0017-capacity-lease-controller-over-cli-saga.md) requires.

Three contract terms are stated in the interface and enforced by a conformance suite that every implementation must pass, including the fake used in tests. Create is idempotent on the name and returns the existing instance rather than an error. Get returns a not-found sentinel and nothing else when the instance is absent. Delete is idempotent, and deletion is complete only when a subsequent get reports absence.

The ownership guard from the current Hetzner client survives unchanged in spirit: an implementation must refuse to delete an instance that does not carry horizon's management label. That guard exists because two systems shared a Hetzner project and it prevented a real mistake; widening the interface must not lose it.

The label set applied atomically at create is fixed: `horizon.dev/managed-by=horizon`, `horizon.dev/pool=reserved`, `horizon.dev/expires-at` as a Unix timestamp, `horizon.dev/lease`, and `horizon.dev/lease-uid`. The Hetzner implementation lists and deletes only servers carrying the managed-by label, and separately refuses to create or delete any server that also carries an `hcloud/node-group` label, the marker a Kubernetes cluster-autoscaler places on nodes it owns, so a project shared with an autoscaler is never touched by horizon's sweeps.

Capability reporting earns its place by driving a branch rather than by describing one. The controller refuses to provision capacity from a provider that cannot self-terminate unless a node credential has been configured, so a deployment that could not guarantee teardown fails at configuration time instead of at deadline. A provider that cannot carry labels cannot participate in the deadline-on-the-resource scheme from [0017](0017-capacity-lease-controller-over-cli-saga.md), and says so at startup rather than at teardown. The check is confined to acceptance: teardown, expiry, and orphan collection build the same provider client and keep working regardless, so capacity that already exists is still released and a configuration change never strands a running server.

Hetzner is the first implementation, and is chosen first precisely because it is the hardest case: it has no provider-side deadline and no credential-free self-termination, so building against it forces the compensating machinery to be correct rather than optional.

Configuration is a single `ProviderConfig` resource carrying mutually exclusive typed blocks, one per provider, with a discriminator field and a validation rule requiring exactly one to be set. Each block holds only what that provider needs: Hetzner takes a token reference, an image selector, SSH keys and a node-credential reference, while a provider that self-terminates without credentials would have no node-credential field at all. Credentials are always references to Secrets and never inline values.

Karpenter's alternative, a separate resource per provider, exists to support providers shipped out of tree by other authors. That is not the situation here, and one resource with typed blocks is simpler to install and to validate. If out-of-tree providers ever matter, splitting the blocks into separate resources is a mechanical migration rather than a redesign.

## Options considered

- Keep the two-method interface and special-case the lease logic above it. Rejected: the controller would need to know which provider it was talking to in order to know how teardown is enforced, which is the coupling the seam exists to prevent.
- Model the interface on Cluster API's infrastructure contract. Well specified and battle-tested, but it assumes the surrounding Cluster API object graph and its versioned contracts, which is disproportionate here.
- Adopt a general multi-cloud provisioning library rather than writing adapters. Rejected because no maintained Go equivalent exists. The nearest candidate was archived in July 2025 with no successor, and the Go Cloud Development Kit covers storage and messaging but not compute. Every comparable project writes per-provider adapters, which is roughly two to four hundred lines each.
- Report capabilities as documentation rather than as a value in the interface. Rejected: an unenforced capability is a comment, and the difference here has a security consequence that should fail closed.

## Consequences

A second provider is a new implementation of one interface plus its own configuration block, and the conformance suite tells the author when it is wrong. The interface can now express the difference between providers that guarantee teardown and providers where horizon must guarantee it, which is the difference the tool is about.

The Hetzner path acquires an obligation the others do not have. A delete-capable token must reach an ephemeral machine that also runs migrated workloads. The mitigation is a dedicated Hetzner project containing only ephemeral capacity and the node images, so the blast radius of a leaked token is the set of resources that were going to be destroyed anyway, and the node images are rebuilt from a content hash by CI. This is recorded here rather than left implicit, because it is a security compromise accepted for a billing reason and it should be visible.

This record supersedes the interface decision in [0016](0016-cluster-agnostic-tool-with-provider-seam.md). The cluster-agnostic behaviour from that record stands, as does its rejection of a plugin framework: this widening is driven by a requirement that exists, not by an abstraction that might one day be useful.
