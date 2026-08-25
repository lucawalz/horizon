---
status: accepted
date: 2026-08-25
---

# 0030. Define the verification gates once in the Makefile

## Context

Continuous integration and the Makefile each carried their own list of what must pass before a change is good. The two lists were written independently and nothing compared them.

The Makefile knew about `test`, `test-race`, `vet`, `manifests` and `chart-lint`. `.github/workflows/ci.yaml` knew about those and also about `go mod tidy -diff`, `golangci-lint run`, `bash scripts/check-adr-index.sh`, `goreleaser check`, the assertion that the chart's rendered image tag equals its `appVersion`, two guards keeping every vite server on loopback, and a comparison of the committed `internal/web/site/dist` against a fresh build. Six gates existed only in the workflow file, and the only way to discover one was to break it.

Tagging v0.10.0 took three attempts for that reason. Each attempt failed on a gate the local toolchain does not run, and each fix needed a deleted and re-pushed tag. The gates were correct; nothing local reached them.

[0024](0024-validate-the-release-configuration-before-the-tag.md) is the same defect one layer up. The release configuration was first read at tag time, so a malformed file failed after two immutable artifacts were already public. That record moved the check into continuous integration and closed the gap between the release workflow and the pull request. The gap between the pull request and the working copy stayed open, and this record closes it.

Two of the gates also floated. `golangci/golangci-lint-action` was pinned to `latest` and `goreleaser/goreleaser-action` to `~> v2`, so both tools could move without a commit. A linter that acquires a new check overnight fails a pull request that changed nothing related to it, and the failure carries no diff explaining where it came from.

## Decision

The Makefile is the single definition of the verification gate set. Every gate is a target, `verify` chains them, and continuous integration invokes the targets rather than restating their contents.

Each job keeps only what a runner needs and nothing about what is checked: the checkout, the toolchain setup, the caches, and one `make` call per gate. The `test` job runs `tidy-check`, `vet`, `build`, `test` and `test-race`; `lint` runs `adr-check` and `lint`; `site` runs `site`; `release-config` runs `release-check`; `chart` runs `chart-lint`. Job names are unchanged because branch protection names `test` and `chart`.

`golangci-lint-action` and `goreleaser-action` are removed rather than kept alongside the targets. Both actions take a version, and a version declared in the workflow next to a version declared in the Makefile is the drift this record exists to remove, in a smaller and harder to notice form.

### Two ways of pinning a tool, chosen per tool

controller-gen, setup-envtest and gofumpt stay `go tool` dependencies resolved through the `tool` block in `go.mod`. golangci-lint and goreleaser run through `go run <module>@<version>` against a version held in a Makefile variable.

The split follows from what joining the main module costs. A `tool` dependency enters the module's requirement graph, so minimal version selection raises every shared dependency it needs. golangci-lint and goreleaser between them pull in `golang.org/x/tools`, the cloud provider SDKs and a large part of the analysis ecosystem, and `golang.org/x/tools` is shared with controller-runtime. Adding either would move the versions the operator itself compiles against, for a tool that never ships in the binary. `go run <module>@<version>` resolves in its own graph, pins exactly, and leaves `go.mod` and `go.sum` untouched. The three small generators cost nothing in the graph and are better off in it, because `go mod tidy` then keeps them current.

goreleaser is pinned to the newest v2 that declares the same Go version `go.mod` declares. The next release requires a newer toolchain, which the Go toolchain would download on demand, and a gate that quietly fetches a second compiler is a gate that behaves differently depending on `GOTOOLCHAIN`.

### One gate that continuous integration never had

`manifests-check` regenerates the custom resource definitions and the deepcopy methods, then fails when the result differs from what is committed. The chart gate already caught stale definitions, but only as a side effect of the definitions being committed twice and compared to each other. Deepcopy output is committed once, so nothing compared it against anything.

### Freshness is read, not staged

The workflow checked the committed bundle by running `git add -A` and then `git diff --cached`, which is a reasonable thing to do to a runner that is deleted afterwards and a hostile thing to do to a working copy. The target reads `git status --porcelain -uall` over the path instead, which reports content-hashed bundles arriving as new files without touching the index.

## Options considered

- Keep the gates in `ci.yaml` and add a `make verify` that repeats them. Rejected: two definitions of the gate set is the defect, and a second copy that drifts silently is what the three failed tag attempts cost.
- Generate `ci.yaml` from the Makefile. Rejected: a generator and a freshness check for its output is more machinery than five `run:` lines, and the workflow still has to say which gate runs in which job.
- Add golangci-lint and goreleaser to the `tool` block for consistency with the generators. Rejected: both would join the requirement graph and move shared dependencies the operator compiles against.
- Keep `golangci-lint-action` for its result caching and teach it to read the version from the Makefile. Rejected: the version would be parsed in one place and declared in another, and a workflow that parses a Makefile is harder to follow than a `run:` line.
- Install both tools from their release binaries in a script. Rejected: it means maintaining a checksum per platform for something the Go toolchain already resolves reproducibly from a version string.
- Have every job run `make verify`. Rejected: it discards the parallelism across jobs and collapses the named checks that branch protection refers to.
- Let `manifests-check` fail on any dirty file rather than on the generated paths. Rejected: it would fail whenever an API type is edited and not yet committed, which is the state the target is most useful in.

## Consequences

`make verify` is the command to run before a tag, and it runs the gates in ascending cost so a stale lockfile or an unlinked ADR fails in seconds rather than after the site bundle is rebuilt. It is slower than the old local set, because the old local set was missing six gates.

Adding a gate is one target. Running it in continuous integration is still a separate line in `ci.yaml`, deliberately: which gate belongs in which job, and which job blocks a merge, is a continuous integration concern rather than a property of the gate.

The first `make lint` or `make release-check` on a cold module cache compiles the tool, which takes a minute or two. Later runs come from the build cache. Continuous integration pays that cost per job unless the `setup-go` cache holds the build cache across runs, which is a real slowdown compared to the prebuilt binary the linter action used to fetch, traded for a version that only moves in a commit.

A linter bump is now a diff. The finding it introduces lands on the pull request that raised the version rather than on whichever unrelated change ran next, and reverting a bad bump is a revert.

One skew is left open. `.github/workflows/release.yml` still installs goreleaser through the action under a `~> v2` constraint, so the release publishes with whichever v2 is newest at tag time while `release-check` validates the configuration against the pinned version. That was not a gap before, because both sides floated together. Pointing the release job at the same pin is the remaining work, and it belongs in a change that can be exercised against a real tag.

`make site` runs `npm ci`, which removes and reinstalls `node_modules`. Running the full `verify` therefore costs a clean install of the frontend dependencies every time, which is what makes the bundle comparison meaningful and what makes the target the slowest one in the chain.
