# 38. Version releases from Conventional Commits with release-please

Date: 2026-11-19

## Status

Accepted

## Context

[0020](0020-chart-yaml-as-the-release-version-source-of-truth.md) made a hand-edited `charts/horizon/Chart.yaml` the source of the released version. Every release took a `release:` commit bumping `version` and `appVersion`, then a pushed `v*` tag, and `release.yml` refused any tag that disagreed with the chart. The version number was a decision a person made and typed twice, and nothing tied it to what the commits since the last tag actually changed.

The repositories under `lucawalz` now share one release standard, kept in `lucawalz/.github`: release-please opens a release PR from the Conventional Commit PR titles merged since the last release, a tag ruleset lets only a GitHub App and the admin create `v*` tags, GoReleaser attaches the archives to a draft release, the archives are attested, and the release is published only after that. The shared workflow covers the GitHub release. It does not build images or push Helm charts.

### Options considered

- Keep the hand-bumped chart and the pushed tag. Rejected: the shared tag ruleset lets only the App create `v*` tags, and the standard exists so every released repository cuts releases the same way.
- Build the image and push the chart from GoReleaser. Rejected: the shared GoReleaser job sets up neither buildx, QEMU nor a registry login, so it would mean forking the shared workflow for one repository.
- Annotate `Chart.yaml` with `x-release-please-version` comments and use the generic updater. Rejected: the `yaml` updater addresses both fields by path without markers in the file. It rewrites the file through a YAML dumper, so `Chart.yaml` is committed in that dumper's formatting and release PRs change only the two version lines.

## Decision

release-please owns the version. `release-please-config.json` uses the `go` release type with `bump-minor-pre-major`, so below 1.0.0 a breaking change bumps the minor version as the existing `0.x` series always has. Two `yaml` extra-files entries write the new version into `version` and `appVersion` in `charts/horizon/Chart.yaml` in the same release PR, so the tree still describes the release it is about to cut and `helm install ./charts/horizon` still resolves a published image. The manifest starts at `0.12.4`, the last tag, so the first release continues the series.

`.github/workflows/release-please.yml` calls the shared release workflow with GoReleaser and attestation of the `horizon_*` assets. `.goreleaser.yaml` attaches to the draft release-please creates and keeps its notes, and generates no changelog of its own.

The image and the chart move to `.github/workflows/publish.yml`, triggered when the release is published. It keeps the guard that the tag equals the chart version, the three semver image tags, both platforms, provenance and SBOM, the chart push, the `release` concurrency group and `workflow_dispatch` for a re-run against a tag.

## Consequences

A release is one merge of the release PR. Tags are never pushed and the chart version is never edited by hand, and the `release` commit type is retired.

The ordering [0020](0020-chart-yaml-as-the-release-version-source-of-truth.md) chose is reversed: the GitHub release is public before the image and the chart exist, for the length of the publish run. A failed publish leaves a release without its image and chart, recovered by re-running `publish.yml` with `workflow_dispatch` on the tag. The draft that 0020 rejected is now the shared flow's way of attesting assets before they are public, and an unfinished draft is cleaned up by hand.

`version` and `appVersion` always move together, so the chart-only revision 0020 kept open is gone.

The GoReleaser version is pinned twice, in the shared workflow and in the Makefile for `make release-check`, which reopens the skew [0030](0030-define-the-verification-gates-once-in-the-makefile.md) closed. Both pins move by hand together. The shared GoReleaser job sets up no Go toolchain, so the `go` line in `go.mod` selects it through Go's toolchain switching rather than through the `go-version-file` that [0024](0024-validate-the-release-configuration-before-the-tag.md) chose. Release notes come from release-please's changelog rather than GoReleaser's grouped one, so 0024's convention of a per-release `release.header` gives way to the `!` marker on the breaking commit, which release-please lists under a breaking changes heading.
