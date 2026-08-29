---
status: accepted
date: 2026-08-22
amended: 2026-08-24
---

# 0027. Let the web interface create and release leases, behind a typed writer and a cross-origin guard

## Context

[0025](0025-replace-server-rendered-interface-with-embedded-spa.md) built the read-only half of the interface and said so in the record rather than leaving it to be discovered: three JSON endpoints, three routes, and a closing sentence that the JSON API "has to stay read-only until the three in-cluster requirements are met, because it is reachable from wherever the interface is". Those three requirements are forward authentication in front of the interface, a network policy restricting the service to the proxy, and an access review for the authenticated user before any mutation.

They gate the in-cluster serving mode, which does not exist. What does exist is `horizon dashboard`, which builds its client from the caller's own kubeconfig and binds `127.0.0.1` with the address constructed inside the server rather than accepted from a caller. In that mode a write is authorised exactly as `kubectl apply` from the same terminal is authorised, by the same credentials, against the same apiserver, with the same RBAC decision. Withholding the write there does not protect the cluster from anything; it protects it from a convenience.

What loopback does introduce is the failure mode the three requirements were never about. Any page in the operator's browser can reach `http://127.0.0.1:8973`. A form post from a page on the public internet carries none of the attacker's credentials and does not need to: the request runs with whatever the local server already trusts, which here is the caller's kubeconfig. Reads are harmless. A create is a machine that costs money, and a delete is a lease released early.

The other half of the context is the product claim. The project's thesis is that capacity is described rather than named, and until now the only way to exercise it was to write YAML. The `status.selection` stanza landed the same day and records what a policy chose, what it beat, how many candidates were offered, how many qualified and why the rest were rejected. None of it was reachable from the interface.

## Decision

**The interface may create and release capacity leases. Whether a given process may is a property of its type, not of a flag.**

### The writer is a separate, optional field with a narrow interface

`web.Options.Client` stays `client.Reader`. It is not widened. A second field, `Writer`, carries a `LeaseWriter`, which is `Create` and `Delete` and nothing else. It is optional. A server built without one serves every read route exactly as before and answers both mutating routes with `501` and a stated reason.

Three properties follow, and they are the reason for the shape. An embedder that supplies only a reader cannot be made to write, because there is nothing to write with. The absence is a legible message rather than a nil dereference at the first POST. And the in-cluster mode, when it is built, needs the access review to run before any write; it can withhold the writer until the review passes and reuse this seam rather than fight it.

`internal/cli` supplies the writer, because that is where the caller's kubeconfig already is.

### The guard is anchored to the address the listener bound

The first check is not on the request's headers at all. A mutating request must be addressed to this server: its `Host` has to name a loopback address, `127.0.0.1` or `localhost`, and the port the listener actually bound, read from `listener.Addr()` in `ListenAndServe` rather than from anything the request carries. A server that never bound an address refuses every mutation, which is the direction that fails safe.

Without that anchor the three checks below are self-referential and the guard is bypassable by DNS rebinding, which was found in review rather than in design. `Host` is a request header and therefore the attacker's to choose. A page served from `http://evil.example:8973`, whose name the attacker rebinds to `127.0.0.1`, is same-origin from the browser's point of view: it may set the custom header with no preflight, the browser sends `Sec-Fetch-Site: same-origin`, and `Origin` equals `http://evil.example:8973`, which equals the scheme plus the `Host` the same request supplied. All three conditions pass and a lease is created with the operator's kubeconfig, with no click from the operator. Chrome's Private Network Access preflight blunts this; Firefox and Safari implement no equivalent, so it cannot be left to the browser. The demonstration was over a real socket, and the regression test is too: `TestTheServedInterfaceAnchorsTheGuardToTheAddressItBound` serves the interface on a free port, creates a lease over the wire at the address it bound, then sends the same request again with the `Host` of a rebound name and asserts both the `403` and that no lease was created.

The two accepted names are safe to accept because neither is attacker-controllable. `localhost` is reserved to loopback by RFC 6761 and browsers hard-code it, and reaching this server under any other name means addressing it as that name, which is cross-origin and therefore preflighted against a server that answers no preflight.

The anchor is not sufficient on its own, and that is the reason the three checks below stay. A request in absolute form, `POST http://127.0.0.1:8973/api/leases HTTP/1.1` sent with a forged `Host` header, takes `r.Host` from the request target rather than from the header, so it satisfies the anchor and is then refused by the `Origin` check; carrying no `Origin` at all, it would be admitted. Nothing browser-driven can produce one, because absolute form is used only towards a configured proxy and never towards an origin server, and any client that can hand-assemble a request like that can read the kubeconfig it would be spending. It is therefore not a hole and needs no code. It is recorded so that the anchor is read as one check among four rather than as a superset of the other three, and so that none of them is later deleted as redundant.

### Behind a proxy the anchor is the configured origin rather than a forwarded header

Serving the interface from inside the cluster puts a reverse proxy in front of it, and there the bound address and the browser's origin are two different things: the listener holds `0.0.0.0:8082` while the browser reaches `https://horizon.example`. Neither the name nor the scheme matches, so the loopback anchor refuses every mutation in that mode. `--external-origin` states the origin the browser reaches, it is parsed once at startup, and a value that is not an absolute origin fails the command alongside the missing OIDC settings rather than leaving the guard silently unanchored.

What the anchor rests on does not change. The origin is process configuration, so no request can move it. The `Host` of a mutating request must equal the host of that origin, and `Origin`, where the browser sends one, must equal the whole of it, scheme included, so a page served over `http` to the same name is refused. `X-Forwarded-Host` and `X-Forwarded-Proto` are read by nothing: anything that can route to the pod writes them itself, and a decision resting on them would be the rebinding hole again with one more hop in front of it. `Sec-Fetch-Site` is still `same-origin` there, because the browser sees one origin and never learns a proxy stands behind it.

Nothing about the loopback path changes when no external origin is configured, which is every `horizon dashboard`. The two accepted loopback names and the port the listener bound still decide on their own. A proxied deployment that was never told its origin falls back to that same loopback rule and therefore refuses every mutation, which is the direction that fails safe.

### Past the anchor, the guard is three checks, and each one is load-bearing on its own

Every mutating request must satisfy all three, and any failure answers `403` with a JSON body naming which:

- a custom request header the interface sets on its own calls. A form post cannot set one at all, and a scripted cross-origin request that tries must first pass a CORS preflight.
- `Sec-Fetch-Site` present and equal to `same-origin`. A missing header is a refusal rather than a pass, which is the direction that fails safe.
- `Origin`, when present, equal to the server's own origin.

No CORS header is served, by anything, ever. The absence of `Access-Control-Allow-Origin` is what makes the preflight fail, so a permissive one added for convenience would remove the protection the first check rests on.

Each check is tested alone, with a request that fails only that one, because a single test that fails all three at once proves almost nothing about any of them.

The Vite dev server cannot satisfy the guard, and no option is added to let it. Its browser origin is the vite port and the server's is the dashboard port, so one of the two is always wrong: with `changeOrigin` the `Host` is rewritten to look local and the `Origin` no longer matches it, and without it the `Host` names the vite port and fails the anchor. It is dropped anyway, so the dashboard sees the address the browser actually used and refuses on the anchor rather than on a header the proxy forged. An always-present configuration surface whose only purpose is to admit an extra origin is worth less than the convenience it buys. The dev server is therefore for reading and for layout work, and mutations are exercised against the binary, which serves the interface from its own origin.

### The form asks for a requirement and makes the unit explicit

Sizing by requirement is the default and naming a machine type is the escape hatch the custom resource already provides. The form carries no catalogue, because a local dashboard holds none, and it does not need one: the controller resolves the requirement against the provider's own list.

`minMemory` is a `resource.Quantity`, and it is where this project has already lost an arm of a measurement campaign. `4Gi` is 4,294,967,296 bytes and a machine advertising 4 GB has 4,000,000,000, so a requirement written as `4Gi` silently excludes it. The unit is therefore a visible choice rather than a suffix a bare number acquires by default, the decimal unit is offered first because that is how a provider quotes memory, and the resolved byte count is shown beside the input before the lease is created.

The form does not restate the CEL rules the apiserver enforces. Bounds appear as native `min` and `max` so they are visible while typing, and the refusal that reaches the operator is the apiserver's own message, forwarded with its status code, because it names the rule that was broken and no client-side guess is more accurate. Every sizing field is immutable after creation, so there is no edit form and none was built.

### Release asks for a teardown rather than performing one

The endpoint deletes the `CapacityLease`. The controller's finalizer turns that into a drain, a provider delete and a release. The response says so, at `202 Accepted`, and the confirmation in the interface says which clock is doing what instead of asking whether the operator is sure: the controller is one clock and the release starts it, the watchdog on each leased node is the second and it is already running on its own deadline. Nothing in the browser destroys a machine.

### The selection stanza is surfaced, and its absence is stated

The lease detail route carries `status.selection`: the strategy, the chosen type and its hourly rate, the runner-up, how many candidates were offered and how many qualified, and the tally of why the rest were rejected. A lease that named `spec.size` has none, because naming a type is not a policy decision, and that is rendered as a stated absence rather than an empty panel.

## Options considered

- Widen `Options.Client` to `client.Client`. Rejected. The read-only guarantee is currently enforced by the type system rather than by convention, and that is a deliberate design statement worth more than one saved field.
- Take a `--read-only` flag instead of an optional writer. A flag is a runtime value an embedder cannot rely on, and the property that matters is that a process without write capability cannot acquire one.
- A CSRF token minted by the server and embedded in the shell. It works, and it costs a token store, a rotation story and a second failure mode, to protect a surface that already knows every legitimate request is same-origin. The header and fetch metadata pair gives the same answer with no state.
- Trust `Origin` alone. Rejected: it is absent on some same-origin requests, and treating an absent header as a pass is the direction that fails open.
- Compare `Origin` against the request's own `Host`, with no anchor. That is what the first implementation did and it is bypassable by rebinding, as above. A comparison in which both sides come from the same request proves only that the request agrees with itself.
- Permit an extra origin by flag or option, so the dev server can mutate. Rejected: a permanent switch whose only effect is to weaken the guard, in exchange for a convenience during development. The external origin above is not that switch. It replaces the anchor in a mode that has no loopback origin to begin with, rather than admitting a second origin beside one that already works, and no setting anywhere turns the guard off.
- Read the origin from `X-Forwarded-Host` and `X-Forwarded-Proto` behind the proxy. Rejected: those headers are written by whatever last handled the request, so any workload that can route to the pod supplies its own anchor and the guard is back to comparing a request against itself.
- Check `Sec-Fetch-Site` alone. Rejected: it is a modern-browser header, and a request from something that is not a browser carries none. Requiring the custom header as well means a non-browser client has to be deliberate.
- Build the create form as an editor for the whole spec, with an update path. Rejected: `providerRef`, `region`, `size` and `requirements` are all immutable by CEL, so an edit form would be a form whose fields the apiserver refuses.
- Offer a catalogue picker in the form, fed by `/api/machines`. It is the natural next step and it is not this step. A local dashboard holds no catalogue at all, so the picker would be empty in exactly the mode this interface runs in.

## Consequences

`horizon dashboard` is no longer read-only, and the README, the command help and the repository map say so. The interface can now spend money, which it could not before, and the mitigation is the one that was already there: it binds loopback, it authenticates as the caller, and the apiserver applies the same RBAC it applies to `kubectl`.

The statement in 0025 that the JSON API has to stay read-only until the three in-cluster requirements are met is amended rather than overturned. It holds for the in-cluster mode, which is what those requirements gate. It does not hold for a loopback process authenticating as the caller, where the write is already the caller's to make. The three requirements remain requirements for in-cluster serving, and the writer seam is how they will be enforced when that mode is built.

A stale bundle is now a visible failure rather than a silent one. The create endpoint refuses unknown JSON fields, so an interface older than the binary serving it is told exactly which field it sent that no longer exists.

The apiserver's refusal reaches the browser with its own status code and message, which includes the resource kind and name as Kubernetes formats them. That is more noise than a hand-written sentence and more accurate than one, and the trade is deliberate.

Nothing in the mutating path is reachable without the writer, so the read-only build and the read-only embedder keep their behaviour exactly. The read routes are untouched by the guard: a bookmark, a curl and a browser address bar all still work, because none of them is a write.

The header name is written once in Go and once in TypeScript, and a bundle that sets a different one would answer `403` to every create and every release while every read carried on working. That shape is too quiet to leave to review, so a test reads the constant out of `internal/web/site/src/lib/api.ts` and out of the built bundle and fails if either disagrees with the guard.

A lease name that no object could carry is refused locally with `400` rather than forwarded. Client-go rejects such a name before it builds a request, and that error carries no API status, so it previously became a `502` and an error log for what is the caller's typo. The same rule applies to the read route, which had the same shape.

## Update 2026-08-24

"The form carries no catalogue, because a local dashboard holds none" and "a local dashboard holds no catalogue at all" were both accurate the day this record was written: `horizon dashboard` was the only process serving the interface, and the catalogue lived in the controller's memory, reachable by nothing else. [0028](0028-serve-the-interface-in-cluster-behind-a-verified-token-and-impersonation.md) added `horizon serve` afterward and wired it to the same absent catalogue as `horizon dashboard`, without this record being revisited. Both statements above therefore now apply to `horizon serve` as much as to `horizon dashboard`, and they hold regardless of where either process runs: the catalogue is held in the controller's own memory, behind a mutex, with no ConfigMap, Secret or other surface any other process can read. Making the Machines route work needs the catalogue published somewhere a reader outside the controller can reach, which is a separate piece of work.

## Update 2026-08-24, the writer seam widens by one typed verb

`LeaseWriter` was `Create` and `Delete` and nothing else, and the property recorded above rests on exactly that: an embedder that supplies only a reader cannot be made to write. Extending a running lease needs a third verb, and the shape of that verb is what decides whether the property survives.

The verb is `Extend(ctx, name, duration)`. It names a lease and the duration that lease should run for. It accepts no object, no patch and no field path. The JSON merge patch that reaches the apiserver is built inside the implementation and carries two things: `spec.duration`, and the `metadata.resourceVersion` the implementation read a moment earlier. Both are computed there and neither can be supplied by a caller, so the body is still not steerable from outside. A general `Update` or a general `Patch` would have handed every holder of a writer the ability to move any field of any object the caller's RBAC reaches, which is a far wider capability than the two verbs this record narrowed the seam to, and it would have made the seam stop describing what a process holding it may do. A purpose-built verb adds one capability and states it in the type, so the seam keeps saying what it said.

One consequence is that a bare `client.Client` no longer satisfies `LeaseWriter`. Both serving modes wrap their client in `web.LeaseWriterFor`, which is the only implementation this repository ships, and `internal/impersonate` wraps the per-request impersonated client in the same way. That constructor takes a reader and a writer rather than a whole `client.Client`, so it states its own reach: the implementation behind the seam cannot touch a status subresource, a scheme or a REST mapper either. That direction is deliberate: the ability to write is now acquired by an explicit construction rather than carried in by a cluster client that happened to be at hand.

So an embedder can now be made to create a lease, release a lease and lengthen the duration of a lease. It cannot be made to move any other field of a `CapacityLease`, to touch any other kind of object, or to write to a lease's status, because none of the three verbs can express any of that. A process handed no writer still answers `501` to all three routes.

The verb only ever lengthens, and the reason is the teardown guarantee rather than the seam. Since [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) made the deadline derive from `spec.duration` on every reconcile, a shorter duration can put that deadline in the past, and the teardown budget is anchored on the deadline, so the whole grace is already spent before teardown starts and the leased nodes are deleted without being drained. The custom resource still accepts a shortened duration, where an operator editing the object is editing the thing itself and can see what it is bound to; a form in a browser cannot carry that context, so the interface declines to be the trigger. Releasing the lease remains the path that gives capacity back early, and it drains. Nothing about the controller changes for this, and the test that pins the shortening behaviour there is untouched.

The comparison lives inside `Extend`, beside the write, and this was the second attempt. The first put it in the HTTP handler, which read the lease and then called a writer that patched unconditionally, and that is not a compare-and-swap: it holds only that each request writes a value longer than the value it read, which is not the same as the stored value only ever growing. A competitor landing in the window between the read and the patch, another interface caller or a `kubectl patch`, is enough to shorten a running lease through an endpoint that answers `202`. Reproducing it took interposing on the reader, and the interleaving is now a test. The durable fix is that `Extend` performs its own read, compares against what that read returned, and sends the resulting patch under the `resourceVersion` that read saw, so the apiserver refuses anything that moved in between. The endpoint turns that refusal into `409` and says what to do about it, rather than retrying underneath a caller who would then be told a duration they never asked for was applied. A retry loop alone would have papered over the same hole. The invariant now belongs to the verb rather than to one caller of it, which matters because the constructor is exported and the verb is reachable in-process.

Two further states are refused before any write is attempted, because the endpoint would otherwise answer `202` and describe something that will not happen. `refreshDeadline` returns early once `Expired` is true, and `Reconcile` branches to teardown once a lease is being deleted, so an extension in either state moves nothing at all. The minute an operator most wants to extend is the minute before expiry, which is the minute the controller flips that condition, so this is not a corner. Both answer `422` and name release and recreate as the path; `409` is kept for the lost update alone, so the two remain distinguishable to a client that has to decide whether retrying makes sense.

A count of seconds is bounded before it becomes a duration. `time.Duration` is nanoseconds in an `int64`, so beyond 9,223,372,036 seconds the conversion wraps and the apiserver ends up quoting back a duration nobody typed. The bound is representability rather than policy: the 5m to 8h rule stays where this record put it, on the apiserver, and the refusal for anything past the bound names the number that was submitted. A missing or null duration is its own `400` rather than falling through to the shortening refusal, which would otherwise lecture about undrained nodes to a request that named no duration at all.

The lease detail route carries `backstopAt`, the earliest lifetime backstop latched by an instance the lease still holds, so a client can offer a ceiling the controller will not clamp rather than a bound that is refused after the fact. It is an instant rather than a headroom in seconds, because every other time this API reports is an instant, a headroom is stale as soon as it is rendered, and the client already holds `acceptedAt` and so can derive the maximum duration exactly. It is absent where no held instance has latched one, and the `ExpiryClamped` condition, which the same response already carries, is what separates a lease holding no machines yet from one whose machines record no backstop at all. Reporting a ceiling in that second state would be inventing one.

The documented user role in `docs/serving-the-interface.md` gains `patch` on `capacityleases`. Without it an in-cluster interface serving a user bound to that role answers the apiserver's own `403` to an extension, naming the user, the resource and the missing verb, which is legible but is a refusal nobody intended.

## Update 2026-08-25, the writer reaches further than this record claims

"It cannot be made to move any other field of a `CapacityLease`, to touch any other kind of object, or to write to a lease's status" is true of `Extend` and false of the other two verbs. `Create` and `Delete` take a bare `client.Object`, so either one will create or delete any object the caller's RBAC reaches, a `ProviderConfig` or a Secret included. What holds the property today is that the two handlers behind those verbs pass a lease, which is a fact about two call sites rather than about the type, and a reader of this record would reasonably have believed otherwise.

[0033](0033-create-a-provider-config-from-the-interface.md) found this while deciding how the interface should create a `ProviderConfig`, and added a second seam whose single method takes the object itself rather than a `client.Object`, so that seam does carry the guarantee this one describes. `LeaseWriter` is unchanged and the statement above stands corrected rather than restated: the guarantee is currently held by convention for two of its three verbs.

## Update 2026-08-26, the catalogue lands

The catalogue this record said a local dashboard holds none of is now published. [0029](0029-publish-provider-config-readiness-and-its-catalogue.md) writes the resolved instance types onto `ProviderConfig.status`, the separate piece of work the update above named and left undone. Both `horizon dashboard` and `horizon serve` can read that status without reaching into the controller's own memory, so the machine picker offered as an option and declined above for lack of a source to feed it now has one.
