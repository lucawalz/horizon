---
status: accepted
date: 2026-08-23
---

# 0028. Serve the interface in-cluster behind a verified token and Kubernetes impersonation

## Context

[0025](0025-replace-server-rendered-interface-with-embedded-spa.md) built the read-only interface and named three requirements gating the in-cluster mode: forward authentication in front of the interface, a network policy restricting the service to the proxy, and an access review for the authenticated user before any mutation. [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) let the interface create and release leases and left all three standing, because they gate the mode that did not exist rather than the loopback one that did. This record builds that mode, and it settles what those three requirements become once they meet an implementation.

`horizon dashboard` binds `127.0.0.1` and builds its client from the caller's own kubeconfig. Its authentication is the operating system's: reaching the socket means being the person whose kubeconfig the process is already spending, and a write is authorised exactly as that person's `kubectl apply` is authorised, by the same credentials against the same apiserver. Nothing in the process decides anything, which is the reason there was nothing to configure.

Serving the same routes from inside the cluster changes both halves of that sentence at once. The listener is routable, so whoever reaches the socket is any workload that can route to the pod, which in a cluster is a large and mostly unenumerated set. The credential is the pod's ServiceAccount rather than a person's kubeconfig, so authenticating as the caller no longer names anybody: without a further decision every request would be made under one identity, and that identity would have to hold every permission any caller might ever need. The endpoint spends money at both ends, since a create is a machine that is billed and a delete is a lease released early, so neither direction is safe to leave to whoever arrives first.

The third of 0025's requirements needs restating rather than implementing. An access review describes a shape, and it is the weaker of the two shapes available. A `SubjectAccessReview` asks the apiserver whether an identity would be permitted to do something, and the process then does it under its own credential, so the question and the act are two separate calls with the application's own permissions behind the second one. Impersonation asks the same question by making the apiserver answer it in the ordinary way, on the request that performs the act, with no window between the two and no path that skips the check. What follows therefore does not implement the review that 0025 named. It implements what that review was standing in for.

## Decision

**The interface serves on a routable address only where it can verify who is calling, and it never decides what a caller may do.**

### Widening the binding is a typed choice at the call site

`Server.ListenAndServe` takes a `BindAddress`, whose single field is unexported and whose only constructors are `LoopbackAddress(port)` and `ExplicitAddress(address)`. The zero value carries no address and refuses to listen rather than defaulting to one. `horizon dashboard` calls the first and takes a port rather than an address; `horizon serve` calls the second and states its reach in the same expression that supplies it.

Before this the loopback binding was constructed inside the server, and the property held because no other path existed. It now holds because widening the reach is a differently named constructor, which a reviewer reads in a diff rather than infers from the absence of one. That is the durable form of the same guarantee: a mode that needs a routable address exists, and it cannot be reached by accident from the mode that does not.

### Nothing binds a routable address before a caller can be identified

`horizon serve` validates before it does anything else, and it validates the whole set: the issuer, the audience, the header the token arrives in, the claim carrying the username, and the claim carrying the group memberships. The error names every setting that is empty rather than the first. `--external-origin`, where it is given, has to parse as an absolute origin with no path, query or fragment, and fails the command alongside the missing settings rather than leaving the cross-origin guard silently unanchored.

`web.New` refuses the same settings a second time, so an embedder that builds a server directly cannot skip the check the command performs. The verifier seam refuses on its own account as well: a server whose verifier was never filled rejects every credential rather than admitting one, because an unfilled seam that admits is the failure mode that looks like a working system.

The ordering is the decision rather than the checks. A privileged endpoint that cannot identify its callers must never reach the point of binding, so a missing setting is a startup failure read in a crash loop and not a runtime discovery made from an audit log.

Settings alone were not enough to hold that property. Discovery fetches the issuer's configuration document, but the key set behind it is fetched lazily on the first verification, so a process able to reach one and not the other bound cleanly and answered every request with `401`. Startup now fetches the key set as well and requires at least one asymmetric public key in it, naming both the issuer and the key set address when it cannot get one. An empty key set fails the same way an unreachable one does, because a set with nothing usable in it rejects every token that will ever arrive.

### The key set is discovered from the issuer and cannot be configured beside it

A caller presents a signed JWT in a configurable header, `Authorization` by default. The `Bearer` scheme is stripped where it is present rather than demanded, because a proxy in front may forward the credential bare. The token is verified against the key set named by the issuer's own `/.well-known/openid-configuration` document.

The key set is deliberately not a setting of its own. An issuer taken from one place and a key set taken from another is not a misconfiguration that produces an error; it is a signature verification bypass that produces a working system, because the tokens that then verify are the ones signed by whoever owns the second setting. Discovery makes the issuer the single thing an adopter states, and the key set a consequence of it that no separate value can contradict.

Only asymmetric algorithms are accepted, by allowlist rather than by exclusion. A symmetric signature turns the published key set into a signing key, so a verifier able to check such a token is a verifier able to mint one. Anything outside the allowlist is refused before a signature is examined.

A verified token still has to name somebody. A missing username claim, or one present and empty, is a rejection rather than an anonymous identity, because an empty username is the string the apiserver would otherwise be asked to act as.

### Authorisation is Kubernetes impersonation, so the adopter's own RBAC decides

The verified username and groups are impersonated on every call the request makes to the apiserver. The interface holds no opinion about what a caller may do and has no table of its own to consult. A verified identity with no rights over `capacityleases` reads nothing and creates nothing, and what it receives is the apiserver's own refusal. Admitting a new operator is a RoleBinding in the adopter's cluster rather than a change to horizon, and revoking one takes effect at the apiserver rather than at the next restart of a pod.

This fails closed in the direction that matters. An identity the cluster has never heard of has no permissions, so a token that verifies but names a stranger buys nothing at all.

The chart gives the interface its own ServiceAccount, and the only role in the chart that grants `impersonate` has that account as its only subject. The account holds `impersonate` on `users` and `groups` and holds nothing else whatsoever: it reads no lease and writes no lease under its own name. The controller's ClusterRole is untouched by `ui.enabled` and never gains the verb, the two workloads carry separate accounts with separate bindings, and the chart refuses to render if the two names resolve to the same account, because one shared identity would hand the controller impersonation and hand the interface the permission to delete nodes.

Unrestricted, `impersonate` on users and groups admits every identity in the cluster including a cluster administrator, which makes the interface pod the most valuable target in its namespace. `ui.rbac.impersonateUsers` and `ui.rbac.impersonateGroups` become the `resourceNames` of the two rules, so only the listed names may be impersonated. Narrowing them is the documented recommendation rather than the default, because the set of names is rarely known at install time and a chart that refused to render without them could not be installed at all.

### The cross-origin anchor is the configured origin, and it is not re-decided here

0027 settled the anchor after a reviewer proved a DNS rebinding attack against one derived from the request's `Host`. In served mode the anchor is `--external-origin`, the origin a browser actually reaches, parsed once at startup and therefore unmovable by any request. `X-Forwarded-Host` and `X-Forwarded-Proto` are read by nothing, because whatever can route to the pod writes them itself and a decision resting on them is the rebinding hole with one more hop in front of it. A proxied deployment that was never told its origin falls back to the loopback rule and refuses every mutation, which is the direction that fails safe. Nothing in this record changes any of that; it is restated because the mode that made an external origin necessary is the mode this record builds.

### The chart ships the mode off

`ui.enabled` defaults to `false`. 0025 overturned 0019's decision that the chart enables the in-cluster mode by default, and the default stays off now that the mode exists rather than reverting once it does. An endpoint that creates billable capacity and impersonates cluster identities is opt-in for as long as it exists.

Enabling it without an issuer or an audience is a render failure naming whichever is missing, because an interface that cannot verify a token would serve cluster impersonation to whoever asked. A NetworkPolicy restricting ingress to named namespaces is templated by default with `traefik` as its only entry, and an empty list admits nothing rather than everything. Egress is left alone deliberately: the interface needs the apiserver and the issuer, whose addresses differ from cluster to cluster, and a rule built on a guess would fail closed on a path the chart cannot verify.

## Options considered

- Trust identity headers injected by a proxy, such as a forwarded user and group list. Rejected: a header is worth exactly as much as the last hop that could have written it, and inside a cluster that is any workload able to route to the pod. Nothing at the listener distinguishes a request that came through the proxy from one that did not, so the entire authentication would rest on a network policy being correct in every cluster the chart is ever installed into.
- Verify Authentik's `X-authentik-jwt` header, issued by a proxy provider doing forward authentication. This was the original design, and it was tested on 23 August 2026 and abandoned. A proxy provider signs that token HS256 with the provider's client secret and publishes an empty key set, because `ProxyProviderSerializer` does not expose `signing_key`, so there is no published key to verify against. The only way to verify the token is for the application to hold the same shared secret, which is precisely the symmetric case the algorithm allowlist exists to refuse: a verifier holding the signing key can mint what it verifies, and the identity provider stops being the only thing that can vouch for an identity. A standalone OAuth2 provider does publish a real key set, which is why `oauth2-proxy` sits in front of the interface instead and forwards a token from such a provider.
- Accept a key set URL beside the issuer, for a provider whose discovery document is awkward to reach. Rejected: two settings from two places, with nothing checking that they describe the same issuer, is not a misconfiguration an adopter would ever see. It is a working system verifying tokens signed by whoever owns the second setting.
- Authenticate with the Kubernetes front-proxy and client certificate pattern. Rejected here as it was before: it rests on a certificate signed by a CA the apiserver has been configured to trust, and that configuration is a cluster-level setting an adopter on a managed control plane may not be able to change at all.
- Let the interface act as its own privileged ServiceAccount and decide for itself who may do what. Rejected: it moves the authorisation boundary out of the cluster and into the application. The pod would have to hold every permission any caller might need, and horizon's own code would be the only thing between a caller and all of them, so a bug in that code becomes a privilege escalation rather than a broken page. An adopter asking who may release a lease would have to read horizon to find out, instead of reading their own RBAC.
- Carry the authentication settings in a custom resource rather than in process arguments. Rejected for these settings specifically. Whoever can write the object could repoint the issuer at one they control and mint tokens the interface trusts, so authentication would collapse into write access on a custom resource, with the impersonation permission following it. Process arguments are set by whoever deploys the workload, which is already the boundary the impersonation permission assumes. `ProviderConfig` remains a custom resource and is unaffected, because it is domain configuration the controller reconciles rather than the thing deciding who may reach the controller at all.

## Evidence

The observations below were made on 23 August 2026 against Authentik as deployed on the bedrock cluster. They describe one identity provider at one version, and they are written down so that a later reader can see what was tested rather than re-derive it and reach a different conclusion.

An Authentik proxy provider issues `X-authentik-jwt` signed with HS256, keyed on the provider's client secret. Its published key set is empty, because `ProxyProviderSerializer` does not expose `signing_key`, so nothing the issuer publishes can verify that token and only the shared secret can.

An Authentik OAuth2 provider does publish a real key set. That is the arrangement the interface was built against, with `oauth2-proxy` obtaining a token from such a provider and forwarding it to the interface.

## Consequences

The three requirements 0025 attached to the in-cluster mode are settled rather than carried forward. Forward authentication exists, with the verification performed by the interface itself against the issuer's key set rather than trusted from the proxy. The network policy is templated by the chart, restricted to named namespaces and enabled by default. The access review is replaced by impersonation, which answers the same question on the request that performs the act, and 0025's wording is superseded on that point alone.

The README and the chart README no longer describe in-cluster serving as unbuilt, and both now say that the chart default remains off.

The interface is reachable by everyone the adopter's RBAC admits, and horizon has no way to refuse a caller the cluster accepts. That is the intended shape and it is worth stating as a consequence: misconfigured RBAC is now a horizon exposure as well as a cluster one, and the impersonation permission is the thing to audit first.

One image now produces two workloads with different argv and different identities. The chart's surface grows by a Deployment, a Service, a ServiceAccount, a ClusterRole, a binding and a NetworkPolicy, all of them behind one value, and the two identities have to be kept apart by something stronger than convention, which is why the render fails when they collide.

The interface pod needs egress to the apiserver and to the issuer, and the chart cannot supply that rule because neither address is knowable at packaging time. An adopter running default-deny egress has to write it, and the symptom of not writing it is a pod that starts, fails discovery, and never serves.

`horizon dashboard` is unchanged in every respect. It still binds loopback through the constructor that cannot express anything else, it still authenticates as the caller's kubeconfig, and it gains no authentication settings, because the ones added here would be meaningless where the credential is already a person's.
