---
status: accepted
date: 2026-08-20
---

# 0024. Validate the release configuration before the tag

## Context

ADR 0020 made `charts/horizon/Chart.yaml` the source of truth for the released version and reordered the release job so the GitHub release is created last. Both changes reduced the damage a failing release run can do. Neither addressed when the release configuration is first exercised.

`.goreleaser.yaml` is read for the first time by `goreleaser release --clean`, which is the final step of the release job. Everything before it has already run: the guard, the registry login, the multi-architecture image push to `ghcr.io/lucawalz/horizon`, and the chart push to `oci://ghcr.io/lucawalz/charts/horizon`. A configuration defect therefore surfaces only after both immutable artifacts are public. Continuous integration never reads the file at all.

The v0.5.0 release demonstrated the shape of the problem, though not through the configuration itself. Its run failed on the `go mod tidy -diff` before hook. That same check runs in continuous integration, and it had already failed there on the same commit. The tag was pushed onto a red main, and recovery meant deleting the tag, fixing the lockfile and pushing the tag again. The check was present and correct; what was missing was any barrier between a failing check and a tag.

Two further defects were latent at the time this decision was written, and neither was reachable by any existing gate.

The release job resolved its toolchain with `go-version: stable` while continuous integration resolved it with `go-version-file: go.mod`. The two can differ, and `go mod tidy` output depends on the toolchain. A green continuous integration run therefore did not imply that the identical check would pass in the release job, which is precisely the gap that let the v0.5.0 failure go unnoticed until the tag was already pushed.

`.goreleaser.yaml` also carried a `release.header` describing v0.5.0's breaking change to `horizon cloud-init`. Prose about one release living in permanent configuration is stale from the moment that release ships. Left in place it would have republished v0.5.0's breaking change in v0.6.0's notes as though it were new, and no check anywhere compares release notes against the commits they cover.

## Decision

The release configuration is validated on every pull request rather than at tag time. A `release-config` job in continuous integration runs `goreleaser check`, which parses and validates the configuration without building or publishing anything. A malformed configuration now fails on the pull request that introduces it.

The release job resolves its Go toolchain from `go-version-file: go.mod`, matching continuous integration. The `go mod tidy -diff` check that runs in both places now runs under the same toolchain in both places, so passing it once means something about passing it again.

`.goreleaser.yaml` carries no `release.header`. The generated changelog, grouped from commit messages, describes the release. A header is added deliberately in the commit that bumps `Chart.yaml` when a release genuinely breaks something, and removed once that release has shipped. Keeping per-release prose in permanent configuration guarantees it is wrong for every release after the one it was written for.

## Options considered

- Add a step that compares the `release.header` against the commit range since the previous tag and fails when it looks stale. Rejected: there is no reliable signal for what counts as stale prose, and a check that cannot state its own pass condition is worse than a convention. Removing the field removes the failure mode outright.
- Move `goreleaser check` into the release job, before the image and chart pushes. Rejected as insufficient rather than wrong. It would prevent a half-published release, but only by failing the run after the tag is already pushed, which still costs a deleted and re-pushed tag. Validating on the pull request costs nothing and catches the defect a day earlier.
- Run the full `goreleaser release --snapshot` in continuous integration to exercise the build as well as the parse. Rejected: it duplicates the `image` job's build cost on every pull request to catch a narrower class of defect than `check` already catches, and the archives it would produce are discarded.
- Pin the release job to an explicit Go version rather than reading `go.mod`. Rejected: it reintroduces the divergence in a slower-moving form, because the pin and `go.mod` drift apart silently at the next toolchain bump.
- Require continuous integration to be green on the tagged commit before the release job proceeds. Rejected for this repository. It couples two workflows through the check API for a guarantee that a person reading the run list already has, and it makes a re-run after an unrelated flake require a second green run before the release can retry.

## Consequences

A malformed `.goreleaser.yaml` fails on a pull request instead of after two immutable artifacts are public. The check is a parse and a schema validation, so it catches structural defects and unknown fields; it does not catch a configuration that is valid but wrong, such as building the wrong package.

Release notes now come entirely from commit messages, which raises what a commit message is worth. The changelog filters exclude `chore`, `docs`, `test`, `ci`, `build`, `style` and `refactor`, so a change that matters to users must be typed `feat`, `fix` or `perf` to appear at all.

Announcing a breaking change becomes an explicit act with an explicit undo. Adding the header is part of the version bump commit, and removing it is part of the next one. That is one more thing to remember than a permanent field, and it fails in the direction of a missing announcement rather than a false one.

The two workflows now resolve Go the same way, so the toolchain moves for both when `go.mod` moves. A toolchain bump that breaks tidy output will fail continuous integration first, which is where it should fail.
