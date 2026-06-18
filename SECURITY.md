# Security policy

horizon holds no cloud credentials and never calls a cloud API. It reads and writes Cluster API objects through a kubeconfig; the kubeconfig path and context live in a local `config.yaml` that is gitignored and never committed. The infrastructure provider, managed by Cluster API, owns all cloud and node credentials. No secret is stored in the repository.

## Reporting a vulnerability

Report a suspected vulnerability privately through the "Report a vulnerability" form under the repository's Security tab, rather than opening a public issue. A maintainer will respond there.

## Supported versions

Only the `main` branch is maintained.
