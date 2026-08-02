---
status: accepted
date: 2026-08-02
---

# 0020. Make Chart.yaml the source of truth for the release version

## Context

A tag is meant to produce three artifacts that agree with each other: archives on the GitHub release, a container image at `ghcr.io/lucawalz/horizon`, and a Helm chart at `ghcr.io/lucawalz/charts/horizon`. Until now nothing enforced that agreement, and the release workflow had three separate defects that only show up once someone tries to install what was published.

The version had two sources. `charts/horizon/Chart.yaml` declared `0.1.0`, and the workflow overrode both fields with `helm package charts/horizon --version "$semver" --app-version "$semver"` derived from the tag. The in-repo chart therefore disagreed with every published chart by construction. `helm install ./charts/horizon` resolved an image tag that existed in no registry, because `_helpers.tpl` falls back to `.Chart.AppVersion` when `image.tag` is empty. No check anywhere compared the two, so the drift was permanent and silent.

The workflow also created the GitHub release first, before the image build and the chart push. A failure in either later step left a published release advertising a version whose image and chart did not exist. The release is the artifact users see first and the one that cannot be quietly retracted, so it was in exactly the wrong position.

The only trigger was a tag push. A run that failed halfway could be retried only by deleting and re-pushing the tag, which is how the repository ended up with a deleted `v0.1.0` tag and a deleted release, and with a stale local tag 121 commits behind main. Two tags pushed in quick succession would also have raced on the same GHCR package.

Separately, GoReleaser ran `go mod tidy` as a before hook. That hook rewrites `go.mod` and `go.sum` in the workspace that `docker/build-push-action` later uses as its build context, so the image was built from whatever tidy left behind rather than from the committed lockfile. Tidy is a no-op at the current commit, verified by running it against a scratch copy of the tree, but a hook that can silently mutate the dependency tree between the commit and the image is a supply-chain problem regardless of whether it happens to be inert today.

## Decision

`charts/horizon/Chart.yaml` is the single source of truth for the released version. `helm package` runs without version overrides, and a guard step reads the `version` field from `Chart.yaml`, compares it to the tag with the leading `v` stripped, and fails the run when they differ. The guard runs immediately after checkout, before the registry login and before anything is pushed, so a mismatch cannot half-publish. It also rejects a ref that is not a `v`-prefixed tag, which is what makes a manual dispatch against a branch fail cleanly instead of producing nonsense. The guard publishes the version it read as a step output, so the package and push steps do not read the file a second time.

Bumping the chart version becomes a deliberate commit that precedes the tag. The tree describes the release it is about to cut, `helm install ./charts/horizon` resolves an image tag that exists, and a chart-only revision can later carry a `version` ahead of its `appVersion`, which the override made impossible.

The job publishes in dependency order and creates the GitHub release last: image, then chart, then `goreleaser release --clean`. GoReleaser needs neither the image nor the chart, so this is a plain step reorder with no split and no second build. A failure now leaves at worst an image and a chart with no release pointing at them, which is recoverable by re-running, rather than a release pointing at artifacts that were never pushed.

`workflow_dispatch` is added so a partial run can be repeated against the same tag without deleting and re-pushing it, and a `release` concurrency group with cancellation disabled serialises runs so two tags cannot race on the same GHCR package.

The GoReleaser hook becomes `go mod tidy -diff`, which reports what tidy would change and exits non-zero without writing anything. The check that the committed lockfile is tidy is worth keeping; the rewrite is not. The two changes are independent on purpose: the reorder already moves GoReleaser after the image build, and `-diff` means the hook cannot mutate the build context even if the order changes again.

The `chart` job in continuous integration asserts that the image the chart resolves by default equals the repository from `values.yaml` joined to the `appVersion` from `Chart.yaml`. This ties the three files that decide the default image together, so a pinned `image.tag`, a changed fallback in `_helpers.tpl`, or an edited repository all fail on the pull request rather than at release time.

bedrock consumes the published chart from `oci://ghcr.io/lucawalz/charts/horizon` at a pinned version rather than the tree in this repository. A GitOps reconciler needs an immutable, versioned artifact it can roll back to, and the tree at `main` is neither. Pointing bedrock at a path in a sibling repository would also couple the cluster to this repository's working state, so an unfinished commit here would reconcile into the cluster. The published chart is only trustworthy as a pinning target if it is self-describing, which is the practical reason `Chart.yaml` has to be authoritative rather than a placeholder the workflow overwrites.

## Options considered

- Keep the workflow overrides and treat the tag as authoritative, adding a step that rewrites `Chart.yaml` before packaging. Rejected: the tree still disagrees with the published chart at every commit that is not a tag, and a workflow that edits tracked files during a release is harder to reason about than one that reads them.
- Split GoReleaser so the release is created after the other pushes, using `--skip=publish` and then a second invocation. Rejected as unnecessary. There is no ordering constraint between GoReleaser and the other publishers, so moving the step is sufficient. A split would also have meant either building twice, since `--clean` discards `dist` and the open-source distribution has no resume command, or hand-rolling the release with `gh release create` over `dist/CHANGELOG.md` and losing GoReleaser's own release handling.
- Create the release as a draft first and undraft it at the end. Rejected: a failed run leaves a stale draft to clean up by hand, and the release object still exists before its artifacts do.
- Remove the tidy hook outright. Rejected: nothing else in the repository checks that the lockfile is tidy, and `-diff` keeps that check at no cost.
- Have the continuous integration gate require `version` and `appVersion` to be equal. Rejected: it would forbid the chart-only revision that dropping the override was meant to allow.

## Consequences

A tag `vX.Y.Z` produces a multi-architecture image at `ghcr.io/lucawalz/horizon` tagged `X.Y.Z`, `X.Y` and `X`, a chart at `oci://ghcr.io/lucawalz/charts/horizon` versioned from `Chart.yaml`, and a GitHub release carrying darwin and linux archives for amd64 and arm64. The tag must equal the chart version or nothing is published at all.

Releasing now takes two steps rather than one. Forgetting the bump costs a failed run and a deleted tag instead of a wrong artifact, which is the trade the guard is there to make.

The gate proves that the chart resolves the image tag its `appVersion` names. It cannot prove that image was ever pushed. While `version` and `appVersion` move together this is the same statement, but a chart-only revision has to leave `appVersion` on an already-published tag, because that release will build and push an image tagged from the new chart version while the chart continues to reference the older one.

`v0.1.0` is reusable and is the version this decision ships. A `v0.1.0` tag was pushed in June and later deleted along with its release, and a stale local tag still points 121 commits behind main, but the remote carries no tag and no release, and neither `ghcr.io/lucawalz/horizon` nor `oci://ghcr.io/lucawalz/charts/horizon` exists. Nothing public has ever claimed the version in either namespace, so reusing it makes the first published release the first version number rather than leaving an unexplained gap.

One artifact does still claim `0.1.0`: `ghcr.io/lucawalz/horizon-controller`, published by the June pipeline from a command that no longer exists. It is a different package, so there is no technical collision, but two public packages from one repository both tagged `0.1.0` with unrelated contents is a discoverability trap. Deleting it is a precondition of reusing the version, so that `0.1.0` names exactly one thing.

Because no tag survives on the remote, GoReleaser will find no predecessor and the `v0.1.0` changelog will cover the entire history rather than a range. That is a one-time cost of the deleted tag and needs no workaround.
