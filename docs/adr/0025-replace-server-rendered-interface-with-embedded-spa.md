---
status: accepted
date: 2026-08-21
---

# 0025. Replace the server-rendered web interface with an embedded single-page application

## Context

[0019](0019-replace-terminal-interface-with-web-and-printer-columns.md) chose the web interface as the interface for people and fixed its construction in the same sentence: server-rendered with htmx and embedded assets, with no JavaScript build toolchain. That part shipped. `internal/web` holds five `html/template` files, a vendored copy of htmx, one stylesheet, and three views served read-only on loopback by `horizon dashboard`.

The constraint behind the choice was sound and still is. `go build` has to produce the binary without a second toolchain, because the project's claim is a single static binary and the release depends on it. The release compiles that binary twice from one tag: once for the container image through `docker/build-push-action` against the repository's `Dockerfile`, and once by goreleaser for the published archives. A toolchain the Go build cannot supply itself would have to be installed in both places or the release stops working.

What 0019 got wrong is the step from that constraint to its conclusion. It treated a build toolchain in the repository and a build toolchain in the compile path as the same thing. They are separable, and the separation is a published pattern rather than an invention. PocketBase commits its `ui/dist` directory to its repository and embeds it with `//go:embed all:dist` behind a `//go:build !no_ui` tag, paired with a `no_ui` file that leaves the filesystem nil. Anyone building PocketBase from a checkout compiles the interface without Node installed, because the compiled interface is already in the checkout.

Beszel is the closer peer for what the interface should look like rather than for how it should be built. It is a Go binary embedding a React and Vite frontend for self-hosted infrastructure monitoring, at roughly 24.5k stars, and it builds its interface from shadcn/ui components. It is not a precedent for committing the build output: it ignores `dist` in two `.gitignore` files and rebuilds it in every release and Docker workflow. The distinction matters, because the two projects answer different questions. Beszel answers what an interface of this kind can be made of. PocketBase answers how to keep `go build` sufficient while making it out of that.

Committing generated output needs a freshness contract or it decays into a stale artifact nobody notices. templ documents the one this decision adopts. Its continuous integration guidance states that committing the generated files is common practice, so that the codebase can always be built without running the generator, and it recommends checking them by regenerating and diffing. This repository already commits generated output on the same reasoning: `config/crd/bases` is produced by controller-gen and copied to `charts/horizon/crds`, and continuous integration runs `diff -r` between the two.

That comparison is the weaker of the two available contracts, and the difference is worth naming because this decision does not repeat it. Comparing two committed directories against each other catches a copy that was forgotten. It cannot catch a regeneration that was forgotten, because both sides stay identical when neither is regenerated. Regenerating in continuous integration and failing on `git diff --exit-code` catches both.

The same reading corrects a sentence in 0019's own Consequences, which records that "a template library with a code generation step was rejected for the build step it imposes". That reasoning does not hold. templ's own continuous integration documentation recommends committing the generated files precisely so that no consumer needs the generator, which is the pattern this repository already runs for controller-gen. The build step templ imposes falls on whoever changes a template, not on whoever compiles the binary. Whatever the case against templ was, it was not that. The sentence stays in 0019 unaltered, because a superseded record is kept for the reasoning that was later overturned.

Underneath the component layer sits a second choice with its own evidence. shadcn/ui components are copied into the consuming repository rather than depended on, and they sit on a headless primitive library. Base UI and Radix Primitives are the two candidates, and their maintenance has diverged. Base UI committed on the day this record was written and has released roughly monthly through 2026, from v1.1.0 to v1.7.0. Radix Primitives last committed on 2026-07-31, with zero to three commits a month for most of the preceding year and no tagged releases in 2026. shadcn/ui made Base UI its default on 2 July 2026, and Radix now requires an explicit flag. Radix Colors is a separate package on a separate cadence and continues to supply the palette.

## Decision

The web interface becomes a single-page application, built ahead of time and embedded in the binary.

`internal/web/site` holds a Vite project in React and TypeScript, styled with Tailwind v4. shadcn/ui primitives are copied into `src/components/ui` and owned there rather than depended on, and they sit on Base UI. A token layer at `src/lib/tokens.css` carries the project's own scale, so the components render horizon's typography, spacing and colour rather than shadcn's defaults. The defaults are a starting point for a project that has no look yet, and this one already has one to match.

Three routes are served, matching what the interface already shows: the lease list, one lease in full, and the instance type catalogue. `internal/web/api.go` serves the state behind them as JSON. The `html/template` files and the vendored copy of htmx are deleted.

The built `dist` directory is committed to the repository. `internal/web/embed.go` embeds it with `//go:embed all:dist` under `//go:build !no_ui`, and `internal/web/embed_no_ui.go` under `//go:build no_ui` leaves the filesystem nil for a build that wants the operator without the interface. `go build` therefore needs no Node toolchain, the `Dockerfile` needs no Node stage, and both halves of the release compile from the same checkout they compile from today.

Continuous integration rebuilds `dist` and fails on `git diff --exit-code`. This is deliberately the stronger of the two generated-output contracts the repository now runs, for the reason given above: it catches a forgotten regeneration and not only a forgotten copy.

What is being built is the read-only interface and nothing else. That is stated here rather than discovered later. The mutating half of 0019 remains unbuilt, which is the writing of credentials and provider configuration and with it the wizard's job that 0019 assigned to the interface. In-cluster serving remains unbuilt, and so do the three requirements that gate it: forward authentication in front of the interface, a network policy restricting the service to the proxy, and an access review for the authenticated user before any mutation. `horizon dashboard` stays bound to loopback, and its authentication remains the caller's own cluster credentials.

0019 decided more than the stack, and the rest of it stands. Printer columns on the custom resource remain the highest-leverage interface work and the reason `kubectl get`, k9s, Rancher and Headlamp are useful at once. The non-interactive watch command remains the path that reads at the back of a room and does not need the operator alive. The command line stays small, because the custom resource is the API and a tool with no scriptable surface cannot be tested. The web interface still absorbs the first-run wizard's job once the mutating half lands. The three in-cluster requirements are still requirements rather than hardening, because the endpoint spends money.

## Options considered

- Keep the server-rendered templates and htmx. Rejected: the interface has no component vocabulary. Every control in it is hand-written markup with hand-written styling, and each new view pays that cost again, which is the cost a copied-in primitive set pays once.
- Adopt templ and keep server rendering. Rejected, but not for the reason 0019 gave. templ delivers type-safe markup and would fix the least painful part of the current interface, while leaving the component and interaction layer exactly where it is. The correction above is about the reasoning, not the outcome.
- Build `dist` in the `Dockerfile` and in the release rather than committing it, as Beszel does. Rejected: it puts a Node toolchain in the compile path in two places at once, and `go build` from a clean checkout stops producing a working binary. That is the single-binary claim, and it is worth more than a clean diff.
- Depend on a component library instead of copying primitives in. Rejected: the interface is small, and its components need project-specific behaviour more than they need upstream defaults. Copied components are read, edited and versioned like the rest of the repository, and no upgrade changes what renders without a review.
- Build on Radix Primitives underneath shadcn/ui. Defensible, and the more proven library of the two. Rejected on the maintenance evidence above and on shadcn/ui's own default, which decides where the examples, the documentation and the upstream fixes go. Radix Colors is a separate package and is unaffected.
- Serve the interface as a separate artifact rather than embedding it. Rejected: it turns one binary into a binary plus a static site with a deployment story of its own, for a project whose promise is that there is one thing to install and that it leaves nothing behind.
- Keep shadcn's default tokens instead of a token layer. Rejected: the defaults are recognisable as themselves, and adopting them would make the interface look like a scaffold rather than like the project.

## Consequences

The repository now carries a JavaScript toolchain and a lockfile, and changing the interface means installing dependencies and running a build. That cost is real and it falls entirely on interface work. It does not reach the Go build, the container image, the release, or anyone building from a checkout, which is the reason the output is committed.

Interface changes produce large diffs, because each one carries a rebuilt `dist` alongside its source. Review reads the source and skips the artifact, the same way it already skips `charts/horizon/crds`. The freshness check is what keeps the artifact trustworthy rather than merely conventional.

The `no_ui` tag gives a way to compile the operator without the interface, which keeps the controller buildable when the committed output is missing or broken. It also makes a nil filesystem a state the serving path has to handle rather than assume away.

Owning the copied components means owning their upgrades. There is no dependency bump that brings a fixed primitive in; a fix upstream is a file to re-copy and re-read. That is the trade shadcn/ui exists to make, and it is accepted knowingly.

Base UI is the newer library, and this is a bet on its maintenance rather than on its track record. If it stalls, the exit is a migration of the copied components rather than a dependency swap, and the token layer and the Radix Colors palette survive either way.

A JSON API now exists where the server previously rendered state straight into markup. It is the surface the mutating half of 0019 attaches to when that half lands, and it is also a surface that has to stay read-only until the three in-cluster requirements are met, because it is reachable from wherever the interface is.

This record supersedes [0019](0019-replace-terminal-interface-with-web-and-printer-columns.md) in its choice of stack. Everything else 0019 decided, the printer columns, the watch command, the size of the command line, the wizard's job and the in-cluster authentication requirements, is restated above and continues to hold.
