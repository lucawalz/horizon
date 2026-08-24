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

That comparison is the weaker of the two available contracts, and the difference is worth naming because this decision does not repeat it. Comparing two committed directories against each other catches a copy that was forgotten. It cannot catch a regeneration that was forgotten, because both sides stay identical when neither is regenerated. Regenerating in continuous integration and comparing the result against what is committed catches both.

The same reading corrects a sentence in 0019's own Consequences, which records that "a template library with a code generation step was rejected for the build step it imposes". That reasoning does not hold. templ's own continuous integration documentation recommends committing the generated files precisely so that no consumer needs the generator, which is the pattern this repository already runs for controller-gen. The build step templ imposes falls on whoever changes a template, not on whoever compiles the binary. Whatever the case against templ was, it was not that. The sentence stays in 0019 unaltered, because a superseded record is kept for the reasoning that was later overturned.

Underneath the component layer sits a second choice, live from the moment a headless primitive is first needed. shadcn/ui components are copied into the consuming repository rather than depended on, and they sit on a headless primitive library. There are three candidates, because shadcn/ui supports Radix Primitives, Base UI, which became its default on 2 July 2026, and React Aria, which became a first-class base on 17 July 2026.

Maintenance does not decide between them. Radix Primitives published nine stable versions in 2026, two of them minors, and its commit rate over the three months before this record ran 21, 90 and more than 100 a month, the most active it has been in over a year. Its repository has never used GitHub Releases at any point in its history, which is easy to mistake for an absence of releases; the npm registry and the git tags both show otherwise. Base UI releases roughly monthly. Both are alive, so the choice has to be made on something else.

What decides it is the body of existing answers. shadcn/ui ships every update and every new component for all its supported bases, and its own announcement of the Base UI default states that Radix is not being deprecated and is still used in production, so choosing Radix costs one selection at init and opts out of nothing. Radix is the larger installed base at roughly 10.1M weekly npm downloads against 8.6M, which is a maintenance signal in itself and, more usefully, a far larger stock of published answers to the problems a first interface runs into. Beszel, the peer cited above for what an interface of this kind is made of, is itself on Radix, so its patterns transfer without translation. Against a near deadline that asymmetry settles it.

Radix Colors is a separate package and is unaffected by any of this. Its latest stable release is 3.0.0 from 2023-10-02, with nothing since. It is a frozen palette rather than a dormant library, and being frozen is exactly the property that lets it survive a change of primitive base.

## Decision

The web interface becomes a single-page application, built ahead of time and embedded in the binary.

`internal/web/site` holds a Vite project in React and TypeScript, styled with Tailwind v4. Its components are written directly against a token layer at `src/lib/tokens.css`, which carries the project's own scale, so they render horizon's typography, spacing and colour rather than shadcn's defaults. The defaults are a starting point for a project that has no look yet, and this one already has one to match. No headless primitive library is used, because the read-only surface is three tables, a set of status pills, a `<time>` element, one native `<select>` and one `<input>`. A primitive library would add a dependency without removing any work.

Radix Primitives remains the base for the first primitive that is genuinely needed, and the reasoning below stands unchanged for that moment. It has not bound anything yet. What needs one is the mutating half of the interface, where a dialog, a combobox and a menu all appear. `components.json` is kept because `shadcn add` is how such a component arrives, and `shadcn` stays a dependency because `src/index.css` imports `shadcn/tailwind.css`.

Three routes are served, matching what the interface already shows: the lease list, one lease in full, and the instance type catalogue. `internal/web/api.go` serves the state behind them as JSON. The `html/template` files and the vendored copy of htmx are deleted.

The built `dist` directory is committed to the repository. The embedding follows PocketBase's layout exactly: `internal/web/site/embed.go` and `internal/web/site/embed_no_ui.go` sit inside the Vite project in package `site`, so that `//go:embed all:dist` resolves to `internal/web/site/dist` and the embed path and the build output are the same directory rather than two paths that have to be kept in agreement. `embed.go` carries `//go:build !no_ui`; `embed_no_ui.go` carries `//go:build no_ui` and leaves the filesystem nil, for a build that wants the operator without the interface. `go build` therefore needs no Node toolchain, the `Dockerfile` needs no Node stage, and both halves of the release compile from the same checkout they compile from today.

Continuous integration rebuilds `dist` and fails when the rebuild differs from what is committed. The comparison stages first and reads the index, `git add -A` followed by `git diff --cached --exit-code`, because Vite emits content-hashed filenames: a rebuilt bundle is a new untracked file rather than a modified tracked one, and `git diff` on the working tree does not report untracked files at all. A plain `git diff --exit-code` would pass on precisely the change the check exists to catch. Asserting that `git status --porcelain` is empty is the equivalent form.

That comparison is a byte comparison against a bundler, so it means nothing unless its inputs are pinned. The lockfile pins the dependency graph and a single declared Node version pins the runtime, read by the local build and the continuous integration job from the same place. Without both pins the check fails on which machine ran it rather than on what changed. This is deliberately the stronger of the two generated-output contracts the repository now runs: it catches a forgotten regeneration and not only a forgotten copy.

What is being built is the read-only interface and nothing else. That is stated here rather than discovered later. The mutating half of 0019 remains unbuilt, which is the writing of credentials and provider configuration and with it the wizard's job that 0019 assigned to the interface. In-cluster serving remains unbuilt, and so do the three requirements that gate it: forward authentication in front of the interface, a network policy restricting the service to the proxy, and an access review for the authenticated user before any mutation. `horizon dashboard` stays bound to loopback, and its authentication remains the caller's own cluster credentials.

0019 decided more than the stack, and most of the rest stands. Printer columns on the custom resource remain the highest-leverage interface work and the reason `kubectl get`, k9s, Rancher and Headlamp are useful at once. The non-interactive watch command remains the path that reads at the back of a room and does not need the operator alive. The command line stays small, because the custom resource is the API and a tool with no scriptable surface cannot be tested. The web interface still absorbs the first-run wizard's job once the mutating half lands. The three in-cluster requirements are still requirements rather than hardening, because the endpoint spends money.

Two of 0019's decisions do not survive, and they are settled here rather than left to lapse quietly. The API tracer is dropped. 0019 kept it as a flag on the watch and dashboard commands on the strength of 48 lines that already existed; those lines left with the terminal application, the flag was never added to either command, and the watch command it was half attached to does not exist either. Nothing in the repository carries it. Streaming provider calls during a teardown may well be worth having again, but it would be a new decision with a new justification rather than a survival of this one.

0019's decision that the chart enables the in-cluster mode by default is overturned. The chart templates nothing for that mode, so the decision has never been more than intent, and the intent points the wrong way: an endpoint that creates billable capacity should not be on by default, least of all before the three requirements that gate it exist. When the mode lands it is opt-in and the chart's default stays off.

## Options considered

- Keep the server-rendered templates and htmx. Rejected: what exists is a class vocabulary, not a component vocabulary. `internal/web/assets/style.css` does carry reusable classes, `.badge`, `.pill`, `.notice`, `.summary`, `.picker`, `.stamp`, `.phase` with a modifier per phase, and shared table rules, and the templates do factor out partials. They are untyped CSS classes with nothing to enforce their use, and the whole interface holds three interactive controls, one select, one input and one button, all inside the single form in `machines.html`. There is no menu, dialog, combobox or table primitive, so every view past these three builds its own, which is the cost a copied-in primitive set pays once.
- Adopt templ and keep server rendering. Rejected, but not for the reason 0019 gave. templ delivers type-safe markup and would fix the least painful part of the current interface, while leaving the component and interaction layer exactly where it is. The correction above is about the reasoning, not the outcome.
- Build `dist` in the `Dockerfile` and in the release rather than committing it, as Beszel does. Rejected: it puts a Node toolchain in the compile path in two places at once, and `go build` from a clean checkout stops producing a working binary. That is the single-binary claim, and it is worth more than a clean diff.
- Depend on a component library instead of copying primitives in. Rejected: the interface is small, and its components need project-specific behaviour more than they need upstream defaults. Copied components are read, edited and versioned like the rest of the repository, and no upgrade changes what renders without a review.
- Build on Base UI. It is shadcn/ui's default, it is actively maintained, it has released roughly monthly through 2026, and it is a genuinely defensible choice rather than a straw one. Rejected on risk asymmetry against a near deadline: Radix carries a far larger body of existing answers, and this interface needs to be finished more than it needs to be current. If Base UI becomes the mainstream, the exit is re-copying the owned component files, because shadcn components are copied in rather than depended on.
- Build on React Aria, which shadcn/ui made a first-class base on 17 July 2026, selectable wherever the other two are. Rejected on the same asymmetry and more sharply, because it is the most recent of the three bases and the one with the least written around it. Its depth of interaction and accessibility behaviour is real and is not what decides this interface, which is three views of tables and one picker.
- Serve the interface as a separate artifact rather than embedding it. Rejected: it turns one binary into a binary plus a static site with a deployment story of its own, for a project whose promise is that there is one thing to install and that it leaves nothing behind.
- Keep shadcn's default tokens instead of a token layer. Rejected: the defaults are recognisable as themselves, and adopting them would make the interface look like a scaffold rather than like the project.

## Evidence

The external observations below were made on 2026-08-21. They are counts, cadences and release dates, all of which move, and they are written down so that a later reader can see what was true when the decision was made rather than re-derive it and reach a different conclusion.

PocketBase commits its `ui/dist` directory and carries the build-tag pair this record copies: `//go:embed all:dist` under `//go:build !no_ui`, and a `no_ui` file that leaves the filesystem nil.

Beszel is a Go binary embedding a React and Vite frontend for self-hosted infrastructure monitoring, at roughly 24.5k stars, built from shadcn/ui components on Radix Primitives. It ignores `dist` in two `.gitignore` files and rebuilds it in every release and Docker workflow.

Radix Primitives published nine stable versions to npm in 2026, from 1.5.0 on 2026-06-06 to 1.6.7 on 2026-07-24, two of them minors, with a matching git tag `1.6.7` dated 2026-07-25. The repository has published no GitHub Releases at any point in its history, which is where the mistaken impression that it stopped releasing comes from. Its commits ran 0 in April, 21 in May, 90 in June and more than 100 in July.

Base UI has released roughly monthly through 2026, from v1.1.0 to v1.7.0, and became shadcn/ui's default base on 2 July 2026. Interactive `shadcn init` still offers Radix as a choice with Base UI preselected; an explicit `--base` flag is needed only non-interactively. React Aria became a first-class base on 17 July 2026, selectable as `--base aria` anywhere the other two are.

shadcn/ui's announcement of the Base UI default states that Radix is not being deprecated, that every update and every new component ships for both libraries, and that Radix is still used in production.

Weekly npm downloads stand at roughly 10.1M for Radix Primitives against 8.6M for Base UI.

`@radix-ui/colors` is at 3.0.0, published 2023-10-02, with no release since.

## Consequences

The repository now carries a JavaScript toolchain, a lockfile and a pinned Node version, and changing the interface means installing dependencies and running a build. That cost is real and it falls entirely on interface work. It does not reach the Go build, the container image, the release, or anyone building from a checkout, which is the reason the output is committed.

Interface changes produce large diffs, and content-hashed filenames make them churn rather than edits: a rebuild adds files and deletes files instead of modifying them. Review reads the source and skips the artifact, the same way it already skips `charts/horizon/crds`. The staged comparison is what keeps the artifact trustworthy rather than merely conventional.

The `no_ui` tag gives a way to compile the operator without the interface, which keeps the controller buildable when the committed output is missing or broken. It also makes a nil filesystem a state the serving path has to handle rather than assume away.

Nothing is copied in yet, so nothing is owned yet. The first component to arrive through `shadcn add` brings that ownership with it: there is no dependency bump that brings a fixed primitive in, and a fix upstream is a file to re-copy and re-read. That is the trade shadcn/ui exists to make, and it is accepted in advance.

Choosing Radix takes the larger body of existing answers over the library shadcn/ui now preselects, and it accepts that the default points elsewhere. If the centre of gravity moves to Base UI, the migration is a re-copy of the owned component files rather than a dependency swap, and the token layer and the frozen Radix Colors palette survive it either way.

A JSON API now exists where the server previously rendered state straight into markup. It is the surface the mutating half of 0019 attaches to when that half lands, and it is also a surface that has to stay read-only until the three in-cluster requirements are met, because it is reachable from wherever the interface is.

## Supersession

This record supersedes [0019](0019-replace-terminal-interface-with-web-and-printer-columns.md), and 0019's status records that. The scope is narrower than a status line can express, which is why it is set out here.

Replaced: how the interface is built. Server-rendered `html/template` and a vendored htmx become a Vite single-page application, built ahead of time and embedded from a committed `dist`.

Dropped: the API tracer, and the chart enabling the in-cluster mode by default. Both are argued in the Decision.

Standing: printer columns on the custom resource, the non-interactive watch command, keeping the command line small, the web interface absorbing the first-run wizard's job, and the three in-cluster authentication requirements being requirements rather than hardening. All five are restated in the Decision so that a superseded record does not orphan them.

0019 is kept rather than deleted. Its reasoning about templ is wrong and is corrected in the Context above rather than edited on the page where it was written, because the argument that was later overturned is the useful part of a superseded record.
