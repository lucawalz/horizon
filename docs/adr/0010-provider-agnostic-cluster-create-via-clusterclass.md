---
status: accepted
date: 2026-06-16
---

# 0010. Make cluster create provider-agnostic through a ClusterClass topology

## Context

The `cluster create` path rendered a Hetzner- and k3s-specific stack. It emitted a Cluster whose `infrastructureRef` named a `HetznerCluster`, a `KThreesControlPlane` carrying an `HCloudMachineTemplate`, and a worker MachineDeployment bound to an `HCloudMachineTemplate` and a `KThreesConfigTemplate`. Those kinds lived as constants in horizon, so the tool encoded one provider and one bootstrapper directly. Cluster API offers managed topologies for exactly this situation: a ClusterClass, authored once, captures the provider templates and the variables that customise them, and a Cluster needs only reference the class by name and supply values. The provider details belong with the operator who owns the substrate (see [0006](0006-cluster-api-operator-pivot.md)), not baked into the client that drives capacity (see [0007](0007-on-demand-pools-via-machinedeployments.md)).

## Decision

`cluster create` renders only a topology Cluster. The Cluster carries `spec.topology`: the ClusterClass name, the Kubernetes version, the control plane replica count, an optional worker MachineDeployment (class, name, and replicas when replicas are greater than zero), and a list of variables built from repeatable `--set key=value` pairs. A variable value is encoded as JSON, passed through when it already parses as JSON and quoted as a JSON string otherwise, so machine type, disk size, and location all flow through one mechanism. horizon names only the ClusterClass and the variable keys; it encodes no provider or bootstrap kinds. The preview, write-to-bedrock, and apply-live modes are preserved, and applying writes the single Cluster object.

A clusterctl flavor path is the fallback. `cluster create <name> --flavor <file> --set key=value` reads an operator-authored template, detects its required `${VAR}` references, fails fast when any are unset, and substitutes the rest through the SimpleProcessor from the cluster-api module's `cmd/clusterctl/client/yamlprocessor` package. The result feeds the same preview, write, and apply modes. This path also keeps provider kinds out of horizon, since they live in the template. The `--class` and `--flavor` flags are mutually exclusive, and one of them is required unless a default class is configured. Defaults for the class, worker class, and version may come from a small `cluster_create` config block and the existing pool version.

## Options considered

- Config-driven kind names with template variants. The provider kinds would move to configuration rather than disappear, so horizon would still assemble a provider-shaped stack and stay coupled to the contract of each kind it names.
- horizon renders the provider templates itself. It would have to model every provider's infrastructure and bootstrap objects, the coupling this record removes, and it would track each provider's API as it changes.
- A ClusterClass managed topology, chosen. horizon references a class by name and supplies variables, and the provider templates live in the operator-authored ClusterClass.
- A clusterctl `${VAR}` flavor substitution, kept as the fallback. It covers substrates that ship a flat flavor template rather than a ClusterClass, again with no provider kinds in horizon.

## Consequences

Cluster creation is provider-agnostic by construction: swapping or adding a provider is an operator change to the ClusterClass or the flavor template, not a horizon change. The operator must author a ClusterClass and enable the `CLUSTER_TOPOLOGY` feature on the management cluster for the primary path, and the variable names horizon passes must match the class. Variable schema validation is best effort and reserved for the apply path where a live cluster is reachable; preview and write pass variables through unvalidated so they never depend on cluster access. The scale, pool, drain, burst, status, backup, and restore paths are unchanged: they continue to operate on MachineDeployments through the same builders, which this change leaves intact. This record supersedes the hardcoded-template portion of [0007](0007-on-demand-pools-via-machinedeployments.md); the two-mode capacity model it established otherwise stands. The ClusterClass and flavor templates live in the [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository. "Provider-agnostic by construction" is scoped to the lifecycle layer: horizon writes no provider or bootstrap kinds, but the variable keys and values it passes through stay provider-specific, so a second cloud still needs its own definitions. [0013](0013-cluster-api-over-terraform.md) records that distinction.
