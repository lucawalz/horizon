# Security policy

horizon holds cloud credentials and calls cloud provider APIs directly. A provider token is resolved at runtime from exactly one of an inline value, a file path, an environment variable, or a Kubernetes Secret, and is never written to the repository. Cluster access is through a kubeconfig whose path and context live in a local `config.yaml` that is gitignored.

A token with permission to create servers also has permission to delete them and to bill the account that owns it. Two consequences follow.

Provider tokens should be scoped to a project or account containing only ephemeral capacity and the node images it boots from. Hetzner tokens in particular are project-wide and offer read-only or read-write permissions with no per-resource scoping, so the project boundary is the only blast radius control available.

A token that reaches a leased machine, which some providers require in order for that machine to destroy itself, should be a separate credential from the one the operator uses, so that it can be rotated independently and distinguished in audit logs.

No secret is stored in this repository. Configuration examples use placeholders.

## Reporting a vulnerability

Report a suspected vulnerability privately through the "Report a vulnerability" form under the repository's Security tab, rather than opening a public issue. A maintainer will respond there.

## Supported versions

Only the `main` branch is maintained.
