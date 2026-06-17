package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
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

func buildScheduleSpecFromValues(cron string, v map[string]string) (velerov1.ScheduleSpec, error) {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		return velerov1.ScheduleSpec{}, fmt.Errorf("--schedule is required")
	}
	template, err := buildBackupSpecFromValues(v)
	if err != nil {
		return velerov1.ScheduleSpec{}, err
	}
	return velerov1.ScheduleSpec{Schedule: cron, Template: template}, nil
}

func buildBSLSpecFromValues(v map[string]string) (velerov1.BackupStorageLocationSpec, error) {
	provider := strings.TrimSpace(v["provider"])
	if provider == "" {
		return velerov1.BackupStorageLocationSpec{}, fmt.Errorf("--provider is required")
	}
	bucket := strings.TrimSpace(v["bucket"])
	if bucket == "" {
		return velerov1.BackupStorageLocationSpec{}, fmt.Errorf("--bucket is required")
	}
	spec := velerov1.BackupStorageLocationSpec{
		Provider: provider,
		StorageType: velerov1.StorageType{
			ObjectStorage: &velerov1.ObjectStorageLocation{
				Bucket: bucket,
				Prefix: strings.TrimSpace(v["prefix"]),
			},
		},
	}
	if cred := strings.TrimSpace(v["credential"]); cred != "" {
		secret, key, ok := strings.Cut(cred, "/")
		if !ok || secret == "" || key == "" {
			return velerov1.BackupStorageLocationSpec{}, fmt.Errorf("invalid --credential %q, want secretName/key", cred)
		}
		spec.Credential = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret},
			Key:                  key,
		}
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

const workerPoolName = "workers"

func (m model) clusterSpecFrom(in clusterCreateInput) (capi.ClusterSpec, error) {
	name := strings.TrimSpace(in.name)
	if name == "" {
		return capi.ClusterSpec{}, fmt.Errorf("name is required")
	}
	class := orDefault(strings.TrimSpace(in.class), m.app.Config.ClusterCreate.Class)
	if class == "" {
		return capi.ClusterSpec{}, fmt.Errorf("--class is required (or set cluster_create.class)")
	}
	version := orDefault(strings.TrimSpace(in.version), m.app.Config.Pools.Version)
	if version == "" {
		return capi.ClusterSpec{}, fmt.Errorf("version is required")
	}
	variables, err := parseSetVars(in.sets)
	if err != nil {
		return capi.ClusterSpec{}, err
	}
	return capi.ClusterSpec{
		Name:                 name,
		Namespace:            orDefault(strings.TrimSpace(in.namespace), m.app.Config.Pools.Namespace),
		Class:                class,
		WorkerClass:          orDefault(strings.TrimSpace(in.workerClass), m.app.Config.ClusterCreate.WorkerClass),
		WorkerName:           workerPoolName,
		Version:              version,
		ControlPlaneReplicas: in.controlPlaneReplicas,
		WorkerReplicas:       in.replicas,
		Variables:            variables,
	}, nil
}

func parseSetVars(sets []string) ([]capi.ClusterVariable, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	out := make([]capi.ClusterVariable, 0, len(sets))
	for _, s := range sets {
		key, value, ok := strings.Cut(s, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --set %q, want key=value", s)
		}
		out = append(out, capi.ClusterVariable{Name: key, Value: value})
	}
	return out, nil
}

func setVarMap(sets []string) (map[string]string, error) {
	vars, err := parseSetVars(sets)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		out[v.Name] = v.Value
	}
	return out, nil
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
