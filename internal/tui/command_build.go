package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func parseList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (m model) poolTargetFrom(poolType, namespace, pool string, replicas int32) (core.PoolTarget, error) {
	name, err := m.app.Config.Pools.Resolve(poolType)
	if err != nil {
		return core.PoolTarget{}, err
	}
	if poolType == "" {
		poolType = m.app.Config.Pools.DefaultType
	}
	t := core.PoolTarget{
		Namespace: m.app.Config.Pools.Namespace,
		Name:      name,
		PoolType:  poolType,
		Cluster:   m.app.Cluster,
		Replicas:  replicas,
	}
	if namespace != "" {
		t.Namespace = namespace
	}
	if pool != "" {
		t.Name = pool
	}
	return t, nil
}

func buildBackupSpecFromValues(v map[string]string) (velerov1.BackupSpec, error) {
	ttl, err := time.ParseDuration(strings.TrimSpace(orDefault(v["ttl"], core.DefaultBackupTTL.String())))
	if err != nil {
		return velerov1.BackupSpec{}, fmt.Errorf("invalid ttl: %w", err)
	}
	snapshot := parseBool(orDefault(v["snapshot-volumes"], "true"))
	storage := orDefault(strings.TrimSpace(v["storage-location"]), core.DefaultStorageLocation)
	spec := velerov1.BackupSpec{
		IncludedNamespaces: parseList(v["include-namespaces"]),
		ExcludedNamespaces: parseList(v["exclude-namespaces"]),
		IncludedResources:  parseList(v["include-resources"]),
		StorageLocation:    storage,
		TTL:                metav1.Duration{Duration: ttl},
		SnapshotVolumes:    &snapshot,
	}
	if sel := strings.TrimSpace(v["selector"]); sel != "" {
		ls, err := metav1.ParseToLabelSelector(sel)
		if err != nil {
			return velerov1.BackupSpec{}, fmt.Errorf("invalid selector %q: %w", sel, err)
		}
		spec.LabelSelector = ls
	}
	return spec, nil
}

func buildRestoreSpecFromValues(fromBackup string, v map[string]string) (velerov1.RestoreSpec, error) {
	mapping := map[string]string{}
	for _, raw := range parseList(v["namespace-mappings"]) {
		parts := strings.Split(raw, ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return velerov1.RestoreSpec{}, fmt.Errorf("invalid namespace mapping %q, want old:new", raw)
		}
		mapping[parts[0]] = parts[1]
	}
	spec := velerov1.RestoreSpec{
		BackupName:         fromBackup,
		IncludedNamespaces: parseList(v["include-namespaces"]),
	}
	if len(mapping) > 0 {
		spec.NamespaceMapping = mapping
	}
	return spec, nil
}

func (m model) clusterSpecFrom(name, namespace, version, podCIDR, serviceCIDR string, replicas int32) (capi.ClusterSpec, error) {
	if strings.TrimSpace(name) == "" {
		return capi.ClusterSpec{}, fmt.Errorf("name is required")
	}
	namespace = orDefault(strings.TrimSpace(namespace), m.app.Config.Pools.Namespace)
	version = orDefault(strings.TrimSpace(version), m.app.Config.Pools.Version)
	if version == "" {
		return capi.ClusterSpec{}, fmt.Errorf("version is required")
	}
	return capi.ClusterSpec{
		Name:             name,
		Namespace:        namespace,
		ClusterName:      name,
		ControlPlaneMode: capi.Managed,
		PodCIDR:          orDefault(strings.TrimSpace(podCIDR), defaultPodCIDR),
		ServiceCIDR:      orDefault(strings.TrimSpace(serviceCIDR), defaultServiceCIDR),
		Version:          version,
		Replicas:         replicas,
		ClusterInfrastructure: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     defaultClusterInfraKind,
			Name:     name,
		},
		Infrastructure: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     defaultInfraKind,
			Name:     name + "-workers",
		},
		ControlPlaneInfra: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     defaultInfraKind,
			Name:     name + "-control-plane",
		},
		Bootstrap: capi.TemplateRef{
			APIGroup: bootstrapGroup,
			Kind:     defaultBootstrapKind,
			Name:     name,
		},
	}, nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func parseBool(s string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(s))
	return v
}

func parseReplicas(s string, fallback int32) (int32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid replica count %q", s)
	}
	return int32(n), nil
}
