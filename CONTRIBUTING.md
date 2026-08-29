# Contributing to horizon

## Prerequisites

- Go 1.26 or newer
- `kubectl` configured against a Kubernetes cluster, for exercising the operator outside the test suite
- `helm` for the chart, and a container runtime with buildx for the image, when changing either
- Node and npm, at the version pinned in `internal/web/site/.nvmrc`, when changing the web interface
- `jq`, for the guard that keeps every vite server on loopback

golangci-lint and goreleaser need no separate install. The Makefile runs both through `go run` at a pinned version, so the Go toolchain fetches and caches them on first use.

## Setup

```bash
go build ./...
```

## Repository layout

The controller is built on controller-runtime, and the provider is a seam rather than a dependency, so no reconciler reaches a cloud SDK directly. [ADR 0018](docs/adr/0018-provider-seam-around-instance-lifecycle.md) records why.

```
api/v1alpha1/       CapacityLease and ProviderConfig types
cmd/horizon/        main entry point
internal/cli/       cobra root, version, controller, dashboard, serve, watchdog, and cloud-init commands
internal/agent/     node-side dead man's switch
internal/manager/   controller-runtime wiring
internal/web/       web interface, json endpoints and the embedded bundle
                    site/ vite, react and typescript project, dist/ committed
internal/oidc/      bearer token verification against the issuer's published key set
internal/impersonate/ per-request cluster client that reaches the apiserver as the end user
internal/controller/  lease reconciler, orphan collector, provider factory
internal/catalogue/ instance types every provider config offers, kept in memory
internal/k8s/       workload migration, placement restore, node drain
internal/cloudinit/ cloud-init join document generator, one file per flavour
internal/provider/  instance lifecycle interface, capabilities, label constants
                    hetzner/ Hetzner Cloud implementation
                    conformance/ contract suite every implementation must pass
                    fake/ in-memory implementation with a create and delete ledger
internal/metrics/   the metric surface and the recorders that move it
internal/testenv/   shared envtest control plane for the integration suites
internal/version/   build stamp
config/crd/bases/   generated custom resource definitions
charts/horizon/     Helm chart for the in-cluster controller and the optional interface
docs/               usage guide, command line reference, and the guide to
                    serving the interface in a cluster
docs/adr/           architecture decision records
```

## Testing

`make verify` runs every gate CI runs except the image build, in ascending cost, and is the command to run before opening a PR and before tagging a release:

```bash
make verify
```

The Makefile is the single definition of that gate set and the CI jobs call the targets, so nothing is enforced in CI that cannot be run locally. [ADR 0030](docs/adr/0030-define-the-verification-gates-once-in-the-makefile.md) records why. Individual gates run on their own while iterating:

```bash
# Unit and integration tests, and the same suite under the race detector
make test
make test-race

# Linting, at the version pinned in the Makefile
make lint

# Chart, including the check that crds/ matches the generated manifests
make chart-lint

# Generated manifests and deepcopy methods match the API types
make manifests-check

# The web interface, including the check that dist/ matches a fresh build
make site

# The container image, the gate verify leaves out because it needs a container runtime
make image
```

`go test ./...` still exits zero, but the controller suite skips every case that needs an apiserver when it cannot find the envtest control plane binaries. `make test` downloads them into `bin/k8s` first and points the suite at them, so it is the only invocation that runs everything.

Changes to the API types need `make manifests`, which regenerates the custom resource definitions and copies them into the chart, and `make generate`, which regenerates the deepcopy methods. `make manifests-check` runs both and fails when the committed output is stale.

## Web interface

`internal/web/site` is the Vite project behind `horizon dashboard`. Its build output is committed at `internal/web/site/dist` and embedded into the binary, so `go build` needs no Node toolchain and a checkout compiles the interface without one.

Changing anything under `internal/web/site` therefore means rebuilding the bundle and committing the result alongside the source change:

```bash
cd internal/web/site
npm ci
npm run build
```

`make site` runs the same sequence the `site` job runs and fails when the rebuilt bundle differs from what is committed. That job is not a required check, so a stale `dist` is reported rather than blocked and rebuilding stays the author's job.

That check is a byte comparison against a bundler, and it only holds while its inputs are pinned. `package-lock.json` pins the dependency graph and `.nvmrc` pins the Node version, which the local build and the CI job read from the same file. Building on another version can produce a difference that has nothing to do with the change.

`npm run dev` serves the interface with hot reload and proxies `/api` to a `horizon dashboard` already running on its default port. The dev server binds `127.0.0.1`, and `make site` fails if that binding leaves `vite.config.ts`, if the file gains a `host` binding pointing anywhere else, or if an npm script passes `--host`, because the dashboard behind the proxy is unauthenticated. `npm test` runs the frontend unit tests and `npm run lint` runs oxlint.

Reads work through the dev server and mutations do not. Creating and releasing a lease is refused there with a `403`, because the browser origin is the vite port and the dashboard's is its own, so the cross-origin guard can never see a matching pair; no option is added to admit the extra origin. Layout and reading work belong on the dev server, and every mutation is exercised against a built binary serving the interface from its own origin. [ADR 0027](docs/adr/0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) records the reasoning.

## Branch naming

horizon follows [Conventional Branch](https://conventionalbranch.org/).

Format: `<type>/<description>`

| Type | Alias | Use case | Example |
|------|-------|----------|---------|
| `feat/` | `feature/` | New features | `feat/orphan-collector-sweep` |
| `fix/` | `bugfix/` | Bug fixes | `fix/lease-finalizer-ordering` |
| `hotfix/` | - | Urgent fixes | `hotfix/lease-teardown-failure` |
| `release/` | - | Release preparation, the Chart.yaml version bump | `release/v0.2.0` |
| `chore/` | - | Non-code tasks (deps, docs) | `chore/update-dependencies` |

Rules: lowercase letters, numbers, and hyphens only, with no uppercase, underscores, spaces, or consecutive hyphens.

## Commit conventions

horizon follows [Conventional Commits](https://www.conventionalcommits.org/).

**Format**: `<type>[optional scope]: <description>`

- Description: brief, imperative, lowercase, 7 to 12 words
- Scope: component name (`api`, `cli`, `controller`, `provider`, `hetzner`, `k8s`, `chart`, and so on)
- No period at end of subject line
- Subject line only, no body, no bullet points

**Allowed types**: `feat` `fix` `chore` `ci` `docs` `refactor` `perf` `test` `build` `release`

`release` is reserved for the chart version bump that precedes a tag, which [ADR 0020](docs/adr/0020-chart-yaml-as-the-release-version-source-of-truth.md) makes part of the release contract.

**Examples**:

```
feat(controller): collect orphaned nodes and expired provider instances
fix(hetzner): name the crd image selector field in create errors
chore(deps): bump k8s client-go to v0.36
```

## Architectural decisions

Significant design choices are documented as ADRs in [`docs/adr/`](docs/adr/). Add or update an ADR when a PR introduces or changes an architectural decision.

## Pull requests

1. `make verify` passes locally
2. Open a PR against `main`
3. Fill in the PR template completely
4. CI must pass before merging

Branch protection on `main` requires only `test` and `chart`. `lint`, `release-config`, `image` and `site` report their result and cannot block a merge, and `.github/workflows/dependabot-automerge.yaml` merges any minor or patch Dependabot update once the required checks pass, so an npm bump can land without `site` having compared the committed `dist` against a fresh build.
