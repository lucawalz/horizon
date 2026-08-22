# Contributing to horizon

## Prerequisites

- Go 1.26 or newer
- `kubectl` configured against a Kubernetes cluster, for exercising the operator outside the test suite
- `golangci-lint` for linting
- `helm` for the chart, and a container runtime with buildx for the image, when changing either
- Node and npm, at the version pinned in `internal/web/site/.nvmrc`, when changing the web interface

## Setup

```bash
go build ./...
```

## Testing

Run the full test suite before opening a PR:

```bash
# Unit and integration tests
make test

# The same suite under the race detector
make test-race

# Linting
golangci-lint run ./...

# Chart, including the check that crds/ matches the generated manifests
make chart-lint
```

`go test ./...` still exits zero, but the controller suite skips every case that needs an apiserver when it cannot find the envtest control plane binaries. `make test` downloads them into `bin/k8s` first and points the suite at them, so it is the only invocation that runs everything.

Changes to the API types need `make manifests`, which regenerates the custom resource definitions and copies them into the chart. CI fails when the two copies diverge.

## Web interface

`internal/web/site` is the Vite project behind `horizon dashboard`. Its build output is committed at `internal/web/site/dist` and embedded into the binary, so `go build` needs no Node toolchain and a checkout compiles the interface without one.

Changing anything under `internal/web/site` therefore means rebuilding the bundle and committing the result alongside the source change:

```bash
cd internal/web/site
npm ci
npm run build
```

The `site` job in CI rebuilds the bundle and fails when it differs from what is committed. It is not a required check yet, as [Required status checks](#required-status-checks) records, so a stale `dist` is reported rather than blocked and rebuilding stays the author's job.

That check is a byte comparison against a bundler, and it only holds while its inputs are pinned. `package-lock.json` pins the dependency graph and `.nvmrc` pins the Node version, which the local build and the CI job read from the same file. Building on another version can produce a difference that has nothing to do with the change.

`npm run dev` serves the interface with hot reload and proxies `/api` to a `horizon dashboard` already running on its default port. The dev server binds `127.0.0.1`, and CI fails if that binding leaves `vite.config.ts`, if the file gains a `host` binding pointing anywhere else, or if an npm script passes `--host`, because the dashboard behind the proxy is unauthenticated. `npm test` runs the frontend unit tests and `npm run lint` runs oxlint.

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

**Allowed types**: `feat` `fix` `chore` `ci` `docs` `refactor` `perf` `test` `build`

**Examples**:

```
feat(controller): collect orphaned nodes and expired provider instances
fix(hetzner): name the crd image selector field in create errors
chore(deps): bump k8s client-go to v0.36
```

## Code quality

All contributions follow these principles:

- **DRY**: extract shared logic; a change happens in one place
- **KISS**: simplest solution that correctly solves the problem
- **SRP**: each function and module has one reason to change
- **Meaningful names**: names reveal intent without needing comments
- **No magic numbers**: use named constants
- **Fail fast**: validate inputs at the earliest possible point
- **Comments**: add only where the intent is not obvious from the code itself, one line max

## Architectural decisions

Significant design choices are documented as ADRs in [`docs/adr/`](docs/adr/). Add or update an ADR when a PR introduces or changes an architectural decision.

## Pull requests

1. Ensure all tests and linting pass locally
2. Open a PR against `main`
3. Fill in the PR template completely
4. CI must pass before merging

### Required status checks

Branch protection on `main` currently requires only the `test` and `chart` checks. `lint`, `release-config`, `image` and `site` report their result but cannot block a merge.

That gap matters most for the frontend. `.github/workflows/dependabot-automerge.yaml` merges any minor or patch Dependabot update as soon as the required checks pass, so an npm bump can land without `site` ever having built `internal/web/site` or compared the committed `dist/` against a fresh build. Until `site` is required, the freshness check cannot stop a stale bundle from reaching `main`.

A repository admin closes the gap by adding `site` to the required checks:

```bash
gh api --method PATCH \
  repos/lucawalz/horizon/branches/main/protection/required_status_checks \
  --input - <<'JSON'
{
  "strict": false,
  "checks": [
    { "context": "test" },
    { "context": "chart" },
    { "context": "site" }
  ]
}
JSON
```

The endpoint replaces the whole set, so the command restates `test` and `chart` alongside `site`.
