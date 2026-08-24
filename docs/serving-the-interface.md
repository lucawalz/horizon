# Serving the web interface in a cluster

`horizon serve` and `horizon dashboard` serve the same web interface and differ in who is trusted and how. The local dashboard trusts whoever reaches the socket, because on loopback that is the person whose kubeconfig it is already spending. `horizon serve` binds a routable address and trusts nobody by default: it verifies a signed token on every request, impersonates the identity that token names, and lets the cluster's own RBAC decide what happens next.

The chart ships the mode off. Enabling it is a deliberate step, and it cannot be taken without configuring an identity provider first. The reasoning behind every choice described here is recorded in [ADR 0028](adr/0028-serve-the-interface-in-cluster-behind-a-verified-token-and-impersonation.md), and the cross-origin guard it inherits is recorded in [ADR 0027](adr/0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md).

## What the interface expects

Every request has to carry a signed JWT in a configured header, `Authorization` by default, with or without the `Bearer` scheme. The token has to be issued by the configured issuer and name the configured audience, and it has to be signed with an asymmetric algorithm using a key the issuer publishes.

The key set is discovered from the issuer's own `/.well-known/openid-configuration` document and is not configurable separately. An issuer stated in one place and a key set taken from another would verify tokens minted by whoever owned the second setting, which is a signature verification bypass rather than a misconfiguration, so the issuer is the single value an adopter states and the key set follows from it.

Two claims are read off the verified token: one naming the user and one listing group memberships, `preferred_username` and `groups` by default. The username claim has to be present and non-empty or the token is rejected. The groups claim may be absent, which yields an identity with no group memberships.

## Enabling it

```yaml
ui:
  enabled: true
  oidc:
    issuer: https://sso.example.com/application/o/horizon/
    audience: horizon
  externalOrigin: https://horizon.example.com
  rbac:
    impersonateGroups:
      - platform
```

Every key in that example, and the rest of the `ui.*` set including the port, the service type, the resources and the security contexts, is described once in the [chart README](../charts/horizon/README.md#web-interface). That table is the single reference for what a value does and what it defaults to. What follows is what the values mean for an adopter putting the interface in front of an identity provider.

Two guards make a half-configured install fail early rather than serve. The chart refuses to render when `ui.enabled` is set and either OIDC value is empty, naming whichever is missing, and the command itself refuses to bind a routable address unless the issuer, the audience, the header and both claim names are all set. An interface that cannot verify a caller never reaches the point of listening.

Startup also proves the key set is usable rather than assuming it. The command fetches the address the discovery document names and requires at least one asymmetric public key in it, so an unreachable key set and an empty one both stop the process with an error naming the issuer and the address it read. Without that fetch the key set is first read on the first request, which turns either fault into a healthy pod answering `401` to everything.

Every one of these settings is a process argument rather than a cluster object. Whoever can write a custom resource cannot repoint the issuer, so authentication never collapses into write access on an object, and changing who is trusted means changing the deployment.

The interface pod needs egress to the apiserver and to the identity provider. The chart templates no egress rule, because neither address is knowable at packaging time and a rule built on a guess would fail closed on a path the chart cannot verify. Under a default-deny egress policy that rule has to be written by hand, and the symptom of forgetting it is a pod that starts, fails discovery, and never serves.

## Putting a verified token in front of it

The interface verifies the token itself, so anything that delivers a valid token works and nothing about the delivery mechanism is trusted. Any OIDC provider publishing a discoverable key set is usable, and no product is required.

`oauth2-proxy` is one supported arrangement and not a dependency. Deployed in front of the interface, it authenticates the browser against the provider, obtains a token, and forwards it in the header the interface reads. Whatever sits in front has to satisfy three things:

- the forwarded credential is a JWT issued by the same issuer configured in `ui.oidc.issuer`, signed asymmetrically with a key that issuer publishes,
- the token names the audience configured in `ui.oidc.audience`,
- the token carries the username claim, and the group claim where group-based RBAC is intended.

Provider-side, a proxy or forward-auth provider is often the wrong object to configure. Some publish no usable key set at all and sign their forwarded token symmetrically with the client secret, which the interface refuses by design, because a verifier holding the signing key can mint what it verifies. A standard OAuth2 or OIDC application object, of the kind that publishes a key set at its discovery document, is the one to create.

A provider that mints a valid token is still only half the arrangement, because the session behind that token expires and something has to renew it. Authentik issues a refresh token only when the client requests the `offline_access` scope and the provider carries the `offline_access` scope mapping. Adding `refresh_token` to the provider's `grant_types` is necessary but not sufficient. Without both, oauth2-proxy holds no refresh token, `--cookie-refresh` is inert, and the session dies when the access token expires, one hour by default.

Nothing reports the fault while it is latent. Sign-in succeeds, every request is served, and the arrangement looks correct until the first access token reaches its expiry. The refusal that follows is the expired case of the second row under [Reading a refusal](#reading-a-refusal), so an expiry caused by a missing scope mapping reads exactly like an ordinary token failure. An interface that works for an hour after each sign-in and then stops is this and not a clock skew.

The `Host` a browser reaches and the address the interface bound are different things behind a proxy, so `ui.externalOrigin` has to state the browser-facing origin exactly, scheme included. Without it the interface still serves every read, and refuses every create, extension and release with `403`. Forwarded headers such as `X-Forwarded-Host` are read by nothing, because anything able to route to the pod can write them.

## Granting the impersonated identity its rights

The interface holds no permissions over horizon's own resources and never acts under its own name. Every call it makes to the apiserver impersonates the identity the token named, so an operator who has not been granted anything sees the apiserver's refusal. Access is granted in the cluster, with ordinary RBAC, and revoked the same way.

`CapacityLease` and `ProviderConfig` are cluster-scoped, so the binding is a ClusterRoleBinding. This role covers everything the interface can do: list and read leases, create one, extend a running one by patching its duration, release one by deleting it, delete the leftover record of one already released, and read the provider configurations the create form offers.

The `namespaces` rule is the one optional entry. With it, the create form suggests the namespaces the signed-in operator may list. Without it the apiserver refuses that one call, the field stays a plain text box, and nothing else changes.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: horizon-lease-operator
rules:
  - apiGroups:
      - horizon.dev
    resources:
      - capacityleases
    verbs:
      - get
      - list
      - watch
      - create
      - patch
      - delete
  - apiGroups:
      - horizon.dev
    resources:
      - providerconfigs
    verbs:
      - get
      - list
      - watch
  - apiGroups:
      - ""
    resources:
      - namespaces
    verbs:
      - list
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: horizon-lease-operator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: horizon-lease-operator
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: platform
```

Dropping `create` and `delete` from the first rule leaves a read-only operator: the interface serves every view and answers a create or a release with the apiserver's refusal. The two directions are worth separating, since a create is a machine that is billed and a delete is a lease released early.

### Narrow the impersonation permission

Left unrestricted, the interface's ClusterRole permits `impersonate` on every user and every group in the cluster, including a cluster administrator. That makes the interface pod the most valuable target in its namespace, since anything that compromises it inherits the ability to act as anybody. The defaults are empty because the set of names is rarely known at install time, not because empty is a good production value.

`ui.rbac.impersonateUsers` and `ui.rbac.impersonateGroups` become the `resourceNames` of the `users` rule and the `groups` rule, so only the listed identities can be impersonated at all:

```yaml
ui:
  rbac:
    impersonateUsers:
      - alice@example.com
    impersonateGroups:
      - platform
```

The two lists are independent, and a list left empty leaves that resource unrestricted. Narrowing both is strongly recommended in production. Where identities are managed by group, listing the groups and leaving the user list empty still permits impersonation of any username, so both lists matter.

## Reading a refusal

| Response | What it means |
| --- | --- |
| `401` naming the header | No credential arrived. The proxy is not forwarding a token, or it is forwarding it in a different header from `ui.authHeader`. |
| `401` reporting a token that could not be verified | A token arrived and failed verification: wrong issuer, wrong audience, expired, signed with an algorithm that is not accepted, or missing the username claim. |
| `403` from the interface, on a create or a release only | The cross-origin guard refused. Usually `ui.externalOrigin` is unset or does not match the origin the browser used, scheme included. |
| `403` from the apiserver, naming a resource | The impersonated identity is not permitted to do this, or the interface is not permitted to impersonate that identity. Both are RBAC. |
| `501` on a create or a release | The process serves a read-only interface and holds no client that may write. `horizon serve` always supplies one, so this belongs to an embedder rather than to a chart install. |
