# Security policy

horizon handles cloud and cluster credentials (Hetzner Cloud, ZeroTier, and the K3s join token). These are read from environment variables or a local `config.yaml` that is gitignored and never committed. No secret is stored in the repository.

## Reporting a vulnerability

Report a suspected vulnerability privately through the "Report a vulnerability" form under the repository's Security tab, rather than opening a public issue. A maintainer will respond there.

## Supported versions

Only the `main` branch is maintained.
