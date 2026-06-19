---
status: accepted
date: 2026-06-19
---

# 0014. Narrow horizon to an on-demand pool scaler

## Context

horizon grew a cluster lifecycle alongside its pool scaling. [0010](0010-provider-agnostic-cluster-create-via-clusterclass.md) added a `cluster create` path that rendered a topology Cluster from a ClusterClass, with a clusterctl flavor fallback, plus `cluster list` and `cluster delete` to round out the lifecycle. In practice the home cluster is declared once and lives in the bedrock git tree, where Flux reconciles it. Standing a cluster up from the CLI duplicated that responsibility, and the two-mode capacity model that defines horizon, the reserved and elastic worker pools on an existing cluster, never depended on it.

The lifecycle surface also carried weight out of proportion to its use. It pulled a flavor processor, a topology builder, a variable encoder, a managed-cluster dashboard panel, and a set of setup-wizard fields for class defaults, none of which the scaling, burst, drain, status, or backup paths touch. Keeping it meant maintaining a second way to do what bedrock already does declaratively.

## Decision

Remove the cluster create, list, and delete feature and narrow horizon to an on-demand worker-pool scaler for an existing cluster.

The pool paths stay whole: scale up and down, burst, drain, the status dashboard, and the Velero backup, restore, and schedule actions are unchanged. The setup wizard stays, minus the class and worker-class fields that only fed cluster create. The dashboard keeps its node, pool, pressure, control-plane, and GitOps panels and drops the managed-cluster list.

The ExternalControlPlane controller stays. The home control plane is externally managed, so Cluster API never marks it initialized on its own, and the controller latches that status so Hetzner workers bootstrap against it. Pool scaling depends on this; cluster creation did not introduce it and removing cluster creation does not retire it.

Cluster creation moves to bedrock GitOps. A new or replacement cluster is declared in the bedrock tree and reconciled by Flux, the same path the permanent cluster already follows, rather than rendered from the horizon CLI.

## Options considered

- Keep the cluster lifecycle in horizon. The status quo. It duplicates a responsibility bedrock already owns declaratively and carries a flavor processor, a topology builder, and a dashboard panel that the scaling paths never use.
- Keep only `cluster list` and `cluster delete` as read and teardown helpers. Lighter than the full lifecycle, but it leaves a half-feature: listing and deleting clusters the CLI no longer creates, against objects Flux owns, with a dashboard panel to maintain for them.
- Remove the lifecycle and route cluster creation through bedrock GitOps, chosen. horizon does one thing, scale pools on an existing cluster, and cluster declaration lives in one place, the git tree Flux reconciles.

## Consequences

horizon's scope is a single sentence again: it adds and removes on-demand capacity on an existing cluster. The flavor path, the topology renderer, the variable encoder, the managed-cluster dashboard panel, and the `cluster_create` config block are gone, and the setup wizard no longer prompts for class defaults. Creating a cluster is now a bedrock change, a declaration in the git tree, not a CLI command.

The ExternalControlPlane controller is unaffected and remains required: without it the externally-managed control plane stays unmarked and workers will not bootstrap. The provider-agnostic claim of [0010](0010-provider-agnostic-cluster-create-via-clusterclass.md), already scoped by [0013](0013-cluster-api-over-terraform.md) to the operations layer, now applies only to pool scaling, since the lifecycle layer it described has left horizon.

This record supersedes [0010](0010-provider-agnostic-cluster-create-via-clusterclass.md). The pool model from [0007](0007-on-demand-pools-via-machinedeployments.md) and the operator stance from [0006](0006-cluster-api-operator-pivot.md) stand.
