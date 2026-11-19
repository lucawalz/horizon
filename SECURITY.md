# Security policy

horizon holds cloud credentials and calls cloud provider APIs directly. A provider token is read at runtime from a single key in a Kubernetes Secret named by a `ProviderConfig` resource, and is never written to the repository. Cluster access is the controller's own in-cluster service account.

A token with permission to create servers also has permission to delete them and to bill the account that owns it. Two consequences follow.

Provider tokens should be scoped to a project or account containing only ephemeral capacity and the node images it boots from. Hetzner tokens in particular are project-wide and offer read-only or read-write permissions with no per-resource scoping, so the project boundary is the only blast radius control available.

A token that reaches a leased machine, which some providers require in order for that machine to destroy itself, should be a separate credential from the one the operator uses, so that it can be rotated independently and distinguished in audit logs.

No secret is stored in this repository. Configuration examples use placeholders.

## Reporting a vulnerability

Report a suspected vulnerability privately through the "Report a vulnerability" form in the repository's Security tab, at `https://github.com/lucawalz/horizon/security/advisories/new`. Do not open a public issue, pull request or discussion for a suspected vulnerability.

A useful report names the affected version or commit, describes the impact, and includes steps or a proof of concept that reproduce the problem.

## Response

- Acknowledgement within 7 days of the report.
- Coordinated disclosure within 90 days of the report, earlier when a fix ships sooner.
- The reporter is credited in the published advisory unless they ask otherwise.

## Supported versions

Fixes land on `main` and reach consumers in the next release. Only the latest release line receives them, and the floating major and minor image tags, such as `0` and `0.12`, move with it. Earlier tags are not backported.
