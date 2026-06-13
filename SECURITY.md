# Security policy

horizon handles cloud and cluster credentials (Hetzner Cloud, the WireGuard hub, and the K3s join token). The Hetzner token and K3s join values are read from environment variables or a local `config.yaml` that is gitignored and never committed. WireGuard burst keypairs are generated in memory per node and the private key is passed to the VM through Terraform; only the public key is registered on the hub. No secret is stored in the repository.

## Reporting a vulnerability

Report a suspected vulnerability privately through the "Report a vulnerability" form under the repository's Security tab, rather than opening a public issue. A maintainer will respond there.

## Supported versions

Only the `main` branch is maintained.
