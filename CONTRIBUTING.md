# Contributing to horizon

## Prerequisites

- Go 1.26 or newer
- `kubectl` configured against a Kubernetes cluster, for exercising the operator outside the test suite
- `golangci-lint` for linting
- `helm` for the chart, and a container runtime with buildx for the image, when changing either

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
