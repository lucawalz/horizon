---
status: accepted
date: 2026-08-02
---

# 0019. Replace the terminal interface with a web interface and printer columns

## Context

[0009](0009-interactive-tui-as-primary-interface.md) made an interactive terminal application the primary interface, and [0011](0011-first-run-setup-wizard.md) added a first-run wizard to it. Together they are 3,816 lines across 27 files including tests, the largest layer in the project, of which the wizard alone is 747.

[0017](0017-capacity-lease-controller-over-cli-saga.md) removes the ground both stand on. Once a burst is a custom resource, an imperative prompt that mutates infrastructure contradicts the declarative model, and a wizard that writes local configuration is configuring state that now lives in a custom resource and a Secret.

That leaves the question of whether the read-only remainder earns its keep, and a survey of comparable projects answers it. An operator shipping its own terminal application is close to unprecedented: a topic search for Kubernetes operators combined with terminal interfaces returns nothing, and across GitHub only thirteen Go files combine a Bubble Tea program with controller-runtime. The nearest precedent, Argo Rollouts, ships both a terminal view and a dashboard, but the terminal side is a three hundred line renderer over a shared view controller while the dashboard command merely serves an embedded application. The terminal path is never a peer implementation.

Meanwhile k9s, at roughly a third of kubectl's installs, already renders arbitrary custom resources. A bespoke terminal interface competes with a tool the audience already has open.

An argument was considered and discarded during this review: that a local terminal interface is needed because it keeps working when the operator is deleted, which is the moment of interest for a tool about teardown. The survey shows this reasoning is post-hoc. Argo CD separates its API server from its controller because they have different scaling models, and Longhorn because its manager is a DaemonSet and its interface is a separate application. No project documents survivability as the driver. A web interface served locally, or as a separate deployment, inherits the same independence without a second implementation.

The cautionary precedent is MinIO's operator, which embedded a console interface and then removed it, the maintainers noting that it had gone unmaintained for years and that operators providing interfaces is uncommon practice. Strimzi and KubeVirt both archived their attempts.

## Decision

Remove the terminal application and replace it with four narrower surfaces.

Printer columns on the custom resource are the highest-leverage work in the project, at roughly thirty lines. A well-chosen set makes `kubectl get`, k9s, Rancher and Headlamp all useful simultaneously, with nothing to maintain. bedrock already runs Rancher, which renders arbitrary custom resources with a table and an editor, so the first interface costs nothing to deploy.

A non-interactive watch command prints the same state as scrolling output. It follows Crossplane's trace command and Argo Rollouts' rollout view: no terminal framework, no keybindings, no layout engine. It works on a projector, over a slow link, in continuous integration, and after the operator has been deleted.

A web interface is the interface for people, server-rendered with htmx and embedded assets, with no JavaScript build toolchain. It is one implementation with two serving modes selected by a flag rather than duplicated: bound to loopback by a local command, or listening in the pod when the chart enables it. The chart enables the in-cluster mode by default, because a URL is a better starting point for someone evaluating the tool than a binary to install.

The command line stays small: the dashboard, the watch, the version, and thin lease verbs that are sugar over applying a resource. It stays because the custom resource is the API and the declarative path has to work, and because a tool with no scriptable surface cannot be tested. That is not a hypothetical. Measuring the leak in [0017](0017-capacity-lease-controller-over-cli-saga.md) required writing a throwaway program to call the orchestration directly, because it was reachable only through the prompt.

The API tracer survives as a flag on the watch and dashboard commands. It is 48 lines and streaming the underlying calls during a teardown is worth keeping.

The web interface also absorbs the wizard's job. A user without a git-driven cluster needs a way to supply provider credentials and machine settings that is not hand-writing a custom resource, so the interface creates the Secret and the provider configuration on their behalf. Credentials are write-only: entered once, stored in a Secret, and never rendered back. Where a provider configuration carries the ownership labels of a git reconciler, the interface refuses to edit it and says why, so that two systems do not take turns reverting each other. bedrock reached the same boundary for Rancher in its own record 0070.

In-cluster serving makes authentication mandatory rather than optional, because an exposed interface that creates leases is an endpoint that provisions billable machines. Three requirements follow and none is skippable: forward authentication in front of the interface, a network policy restricting the service to the proxy so that a spoofed identity header is not a complete bypass, and an access review for the authenticated user before any mutation so the interface cannot be used to exceed what that person could do directly. Local serving needs none of these, because its authentication is the caller's existing cluster credentials.

## Options considered

- Keep the read-only dashboard and add the web interface alongside it. Rejected: every status field would then have two renderers, two sets of documentation and two demonstration scripts, permanently, for one maintainer. The published pattern is that the terminal side survives only as a thin renderer over shared state, never as a peer.
- Keep the terminal application and ship no web interface. Defensible, and terminal-first infrastructure projects exist. Rejected because it does not serve people evaluating the tool, and because the terminal experience users actually want is k9s, which good printer columns deliver for free.
- Ship no interface at all and rely on printer columns alone. This is the majority pattern among operators. Rejected as too austere for a tool whose central artifact is a countdown, where seeing remaining time and cost at a glance is most of the value.
- Move the terminal application to a separate repository as unsupported, following Podman. Rejected as bookkeeping: it keeps the artifact and the maintenance question without keeping a user.

## Consequences

The removal takes out 3,816 lines of terminal code and, with the wizard's configuration fields, the retired pool type and their tests, 5,473 lines in total against 184 added. Roughly 950 lines return as the replacement surfaces, so the project shrinks substantially while gaining a web interface and better terminal ergonomics than the terminal application provided. Weight was not the reason: only 16 of 816 packages in the dependency tree are terminal libraries, and a stripped binary is 54 MB of which the Kubernetes client libraries are the bulk.

The dependency set shrinks in both directions. The web interface is built on the standard library, `html/template` and `net/http` with method-based routing, plus embedded assets and a vendored copy of htmx, so it adds no Go dependency at all. A template library with a code generation step was rejected for the build step it imposes, a client-side reactivity library for its known synchronisation problems alongside htmx, and a newer alternative for having too small an ecosystem to lean on under time pressure. Four terminal libraries and the Cluster API module leave as direct dependencies.

Live demonstration improves. The watch command and the provider's own listing are both readable at the back of a room and neither depends on the operator being alive, which is the moment the tool is about.

The in-cluster interface introduces a security surface the terminal application did not have, and the three requirements above are the cost of that convenience. They are stated as requirements rather than hardening because the endpoint spends money.

This record supersedes [0009](0009-interactive-tui-as-primary-interface.md) and [0011](0011-first-run-setup-wizard.md).
