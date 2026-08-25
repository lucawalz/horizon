---
status: accepted
date: 2026-08-25
amended: 2026-08-26
---

# 0033. Create a provider config from the interface, behind a second narrow writer

## Context

Configuring a provider was `kubectl` only. The interface listed every `ProviderConfig` the cluster held and refused to make one, and the lease create form said so in its empty state, offering an apply command to an operator who had come to the interface to avoid writing YAML. Everything else a lease needs was reachable from the browser.

[0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) built the write half of the interface and stated the property the shape rests on: an embedder that supplies only a reader cannot be made to write, and what a process holding the writer may do is legible in the type of the writer. Adding a second write surface is a test of that property rather than an application of it, because the obvious way to add one is to reuse the seam that already exists.

The seam does not hold as tightly as that record claims. `LeaseWriter.Create` and `LeaseWriter.Delete` take a bare `client.Object`, so either one will create or delete a `ProviderConfig`, a Secret, or anything else the caller's RBAC reaches. Only `Extend` is pinned to a `CapacityLease` by its signature. What keeps the seam honest today is that the two handlers behind those verbs happen to pass a lease, which is a property of two call sites rather than of the type. Reusing `LeaseWriter` for provider configs would have made that gap load-bearing and left the record describing a guarantee the code does not give.

## Decision

**A second seam, `ProviderConfigWriter`, with one method, `Create(ctx, *v1alpha1.ProviderConfig)`.** The parameter is the object itself rather than a `client.Object`, so the kind this seam reaches is stated by the signature and no call site can widen it. `LeaseWriter` is untouched and unreused.

`Server.writers` widens from one writer source to a set holding both, and each mutating route names the source it writes through. The cross-origin guard is unchanged and still runs first for every route; what moves behind it is the check for an absent writer, which now asks about the one seam the route needs. A process holding a lease writer and no config writer therefore serves the three lease routes and answers `501` to `POST /api/providerconfigs`, and the reverse holds too. Both serving modes supply both writers, so the split matters to an embedder rather than to an install.

**Create only. No edit, no delete.** `ProviderConfigSpec` carries no CEL immutability rules, unlike `CapacityLeaseSpec`, so unlike the lease form this is a choice rather than a constraint. It is the right one until the general `Update` question the writer seam exists to avoid is answered deliberately: a verb that moves an arbitrary field of an existing configuration is exactly the capability [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) declined to add, and repointing a live provider's credentials is a larger act than creating a configuration that starts unready.

**The form references Secrets and never creates one.** A configuration needs two to four Secrets, and creating them from the browser would mean granting the impersonated identity `create secrets` in the namespace holding the controller's own credentials. That is a materially larger grant than anything the interface has held, in exchange for a convenience, and no rule anywhere in this change grants the identity anything over Secrets. The usual objection is that the form then cannot check the reference resolves. It does not need to: the controller resolves every reference and reports `Ready=False` with reason `SecretUnresolved` within seconds, which is the same path that answers for a wrong token, an unresolvable image and a missing join token placeholder.

**The reason is carried, not just the status.** The provider config summary gains the `reason` and the `message` of its `Ready` condition, in the shape the lease detail already uses for conditions, and the `cataloguePublished` field the Go response has always emitted is added to the interface's own type, where it was missing. The create form reads the configuration back over the machines route it already polls and renders what the controller found, so a wrong secret name arrives as the controller's own sentence rather than as a guess made in the browser.

**Client-side validation stays native.** Bounds a single field carries appear as `min` and `max` while typing. The watchdog rules that compare three fields against each other cannot be expressed that way at all, and restating them in the browser is what [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) forbade, so the apiserver's refusal is displayed verbatim with its own status code, exactly as the lease form does. The image is named rather than selected, because a name is what an operator has to hand and the selector form stays available to anyone writing the resource directly.

**The user-facing role becomes a chart manifest.** It existed only as YAML pasted inside prose in `docs/serving-the-interface.md`, which is how it came to be a role nothing rendered and nothing checked. The chart renders it as `templates/interface-operator-clusterrole.yaml` under the same `ui.rbac.create` gate as the interface's own role, it gains `create` on `providerconfigs`, and the documentation links the manifest instead of restating it. It is named after the release with `-interface-operator` appended rather than `horizon-lease-operator`, because the role reaches past leases now and the old name would have said otherwise. No binding is rendered: the identities an install should trust are not knowable at packaging time, so the role grants nothing until an adopter binds it.

## Options considered

- Reuse `LeaseWriter.Create`, which already accepts any object. Rejected. It works today and it is the loophole above, so using it would turn a gap into a dependency and leave the seam unable to say what a process holding it may do.
- Add a general `Update` or `Patch` verb and build an edit form. Rejected for the same reason [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) rejected a general update: one verb of that shape hands every holder of a writer the ability to move any field of any object the caller's RBAC reaches.
- Create the referenced Secrets from the form. Rejected. It needs `create secrets` in the controller's own namespace, which is the largest grant the interface would have asked for, to save a `kubectl create secret`.
- Render the cloud-init document over HTTP so the form can produce it. Rejected. It is a fifth write-shaped surface outside the scope of this change, and the document is pasted or referenced instead.
- Restate the watchdog rules in the browser so a refusal never reaches the operator. Rejected. Two of the three compare fields against each other, no native control expresses them, and a client-side copy of a CEL rule drifts from the rule it copies.
- Add a provider config detail route to carry the reason. Rejected. The machines route already carries a summary per configuration and the form already polls it, so a second route would have been a second thing to keep in step for one field.
- Leave the operator role as copy-paste YAML and only add the verb to it. Rejected. Nothing renders it, nothing lints it, and its verbs had already drifted once from what the interface can do.

## Consequences

The interface can now configure a provider, which was the last step of the eight in `docs/usage.md` that had no path through the browser. The Secrets it points at are still made with `kubectl`, and that is deliberate rather than unfinished.

The claim in [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) that the writer "cannot be made to touch any other kind of object" is corrected rather than inherited. It held for `Extend` and never held for `Create` or `Delete`, whose parameter is a `client.Object`. `ProviderConfigWriter` is what the record described, and it is the shape any further seam should take: name the kind in the signature so the type carries the guarantee instead of the call sites. Narrowing `LeaseWriter` the same way is a separate change and is not made here.

A `501` no longer means the process holds no writer at all. It means the process holds no writer for that route, which is what the response now says and what `docs/serving-the-interface.md` records.

An adopter who applied the role from the old documentation has an unmanaged `horizon-lease-operator` in the cluster and a rendered `<release>-interface-operator` beside it. Moving the binding to the rendered role is the upgrade, and until it moves the interface answers the apiserver's own refusal to a provider config create, naming the user, the resource and the missing verb.

The form offers a single provider type and an image named rather than selected, so a configuration that needs `imageSelector`, a numeric image id, or a provider horizon does not yet support is still written with `kubectl`. Every one of those is refused by the apiserver with its own message when the form cannot express it, rather than accepted and silently reshaped.

## Update 2026-08-26, the detail route is added for what the summary cannot carry

"Add a provider config detail route to carry the reason. Rejected" was the right answer to the question it was asked. The reason and the message of `Ready` are one field each, the machines route already carries a summary per configuration, and a second route for them would have been a second thing to keep in step.

It was not the answer to a different question, which is what a configuration is actually configured with. The summary carries a name, a type, two condition statuses and a timestamp, and nothing in the interface showed the image, the SSH keys, the firewalls, the watchdog timings or the Secrets a configuration references. `kubectl` was the only way to see any of it, including for an operator whose configuration is unready because a reference names a Secret that is not there.

`GET /api/providerconfigs/{name}` is therefore added, read-only, served through the same read path as the lease routes and impersonated the same way. It is not behind `mutating`, because that guard exists for writes and a read carries none of the risk it answers. The response mirrors `leaseDetailResponse`, including its `conditions` array, which the lease detail page and this one now render through the same component rather than through two copies of one table.

**Secrets are quoted by name and key and never resolved.** The reference is what tells a missing Secret apart from a misnamed one, and that is the whole of what an operator needs to see here: the controller already reports `Ready=False` with reason `SecretUnresolved` when a reference does not resolve. Reading the Secret would put a provider token in a browser and would need `get secrets` in the namespace holding the controller's own credentials, which is the grant this record refused for creating them. Nothing in the change grants the impersonated identity anything over Secrets, and the property is pinned by a test that asserts each of the four references encodes exactly a name and a key, and by one that serves a configuration referencing a real Secret and fails if its contents appear anywhere in the response.

**A published catalogue is tallied rather than listed.** `MaxPublishedInstanceTypes` is 512, so rendering the catalogue would make it the page. The detail carries the count and a per-region tally, and each region links to the machines route, which is where the instance types are listed and was already built to list them. The absence of a catalogue is stated rather than shown as a tally of zero.

The user-facing role is unchanged. It already granted `get`, `list` and `watch` on `providerconfigs`, so an identity bound to it can open the view with no new rule.

The interface's route for creating a configuration moves from `/configs/new` to `/new-config`. The detail page is at `/configs/{name}`, so leaving the create page inside that prefix would have made `new` a name no configuration could be viewed under, and the leases half of the interface already keeps its create page out of the resource prefix at `/new`.
