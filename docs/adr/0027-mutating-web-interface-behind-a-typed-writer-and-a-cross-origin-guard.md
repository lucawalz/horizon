---
status: accepted
date: 2026-08-22
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
- Permit an extra origin by flag or option, so the dev server can mutate. Rejected: a permanent switch whose only effect is to weaken the guard, in exchange for a convenience during development.
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
