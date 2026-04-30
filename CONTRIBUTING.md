# Contributing to horizon

## Prerequisites

- Go ≥ 1.26
- Terraform ≥ 1.9
- `kubectl` configured against a K3s cluster or the Hetzner eval environment
- `golangci-lint` for linting

## Setup

```bash
go build ./...
```

## Testing

Run the full test suite before opening a PR:

```bash
# Unit and integration tests
go test ./...

# Linting
golangci-lint run ./...
```

## Branch naming

horizon follows [Conventional Branch](https://conventional-branch.github.io/).

Format: `<type>/<description>`

| Type | Alias | Use case | Example |
|------|-------|----------|---------|
| `feat/` | `feature/` | New features | `feat/watch-daemon-hysteresis` |
| `fix/` | `bugfix/` | Bug fixes | `fix/headscale-preauth-revocation` |
| `hotfix/` | — | Urgent fixes | `hotfix/terraform-state-leak` |
| `release/` | — | Release preparation | `release/v0.2.0` |
| `chore/` | — | Non-code tasks (deps, docs) | `chore/update-dependencies` |

Rules: lowercase letters, numbers, and hyphens only — no uppercase, underscores, spaces, or consecutive hyphens.

## Commit conventions

horizon follows [Conventional Commits](https://www.conventionalcommits.org/).

**Format**: `<type>[optional scope]: <description>`

- Description: brief, imperative, lowercase, 7–12 words
- Scope: component name (`cli`, `provider`, `runner`, `k8s`, `headscale`, …)
- No period at end of subject line
- Subject line only — no body, no bullet points

**Allowed types**: `feat` `fix` `chore` `ci` `docs` `refactor` `perf` `test` `build`

**Examples**:

```
feat(provider): add hetzner vm provisioning via terraform-exec
fix(runner): log rollback errors to stderr instead of discarding
chore: bump k8s client-go to v0.35
```

## Code quality

All contributions follow these principles:

- **DRY** — extract shared logic; a change happens in one place
- **KISS** — simplest solution that correctly solves the problem
- **SRP** — each function and module has one reason to change
- **Meaningful names** — names reveal intent without needing comments
- **No magic numbers** — use named constants
- **Fail fast** — validate inputs at the earliest possible point
- **Comments** — add only where the intent is not obvious from the code itself, one line max

## Architectural decisions

Significant design choices are documented as ADRs in [`docs/adr/`](docs/adr/). Add or update an ADR when a PR introduces or changes an architectural decision.

## Pull requests

1. Ensure all tests and linting pass locally
2. Open a PR against `main`
3. Fill in the PR template completely
4. CI must pass before merging
