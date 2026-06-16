package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func (m model) poolUpForm() formState {
	cfg := m.app.Config
	fields := []formField{
		{label: "type", input: newTextInput(cfg.Pools.DefaultType, cfg.Pools.DefaultType)},
		{label: "replicas", input: newTextInput("1", "1")},
		{label: "namespace", input: newTextInput(cfg.Pools.Namespace, "")},
		{label: "pool", input: newTextInput("(default by type)", "")},
		{label: "nudge", input: newTextInput("false", "false")},
	}
	return newForm(actionPoolUp, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		replicas, err := parseReplicas(v["replicas"], 1)
		if err != nil {
			return "", nil, err
		}
		target, err := m.poolTargetFrom(v["type"], v["namespace"], v["pool"], replicas)
		if err != nil {
			return "", nil, err
		}
		nudge := parseBool(v["nudge"])
		prompt := fmt.Sprintf("Scale pool %s/%s up to %d replicas (nudge=%t)?",
			target.Namespace, target.Name, target.Replicas, nudge)
		return prompt, m.runScaleUp(target, nudge), nil
	})
}

func (m model) poolDownForm() formState {
	cfg := m.app.Config
	fields := []formField{
		{label: "type", input: newTextInput(cfg.Pools.DefaultType, cfg.Pools.DefaultType)},
		{label: "namespace", input: newTextInput(cfg.Pools.Namespace, "")},
		{label: "pool", input: newTextInput("(default by type)", "")},
		{label: "delete", input: newTextInput("false", "false")},
	}
	return newForm(actionPoolDown, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		target, err := m.poolTargetFrom(v["type"], v["namespace"], v["pool"], 0)
		if err != nil {
			return "", nil, err
		}
		del := parseBool(v["delete"])
		prompt := fmt.Sprintf("Scale pool %s/%s to 0 replicas?", target.Namespace, target.Name)
		if del {
			prompt = fmt.Sprintf("Delete pool %s/%s entirely?", target.Namespace, target.Name)
		}
		return prompt, m.runScaleDown(target, del), nil
	})
}

func (m model) nudgeForm() formState {
	cfg := m.app.Config
	fields := []formField{
		{label: "namespace", input: newTextInput(cfg.Pools.Namespace, cfg.Pools.Namespace)},
		{label: "cluster", input: newTextInput(m.app.Cluster, m.app.Cluster)},
	}
	return newForm(actionNudge, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		target := core.PoolTarget{Namespace: v["namespace"], Cluster: v["cluster"]}
		if target.Namespace == "" {
			target.Namespace = cfg.Pools.Namespace
		}
		if target.Cluster == "" {
			target.Cluster = m.app.Cluster
		}
		prompt := fmt.Sprintf("Latch control-plane-initialized for cluster %q in %s?", target.Cluster, target.Namespace)
		return prompt, m.runNudge(target), nil
	})
}

func (m model) burstForm() formState {
	cfg := m.app.Config
	fields := []formField{
		{label: "workload", input: newTextInput("namespace to burst (required)", "")},
		{label: "type", input: newTextInput(cfg.Pools.DefaultType, cfg.Pools.DefaultType)},
		{label: "replicas", input: newTextInput("1", "1")},
		{label: "namespace", input: newTextInput(cfg.Pools.Namespace, "")},
		{label: "pool", input: newTextInput("(default by type)", "")},
	}
	return newForm(actionBurst, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		workload := strings.TrimSpace(v["workload"])
		if workload == "" {
			return "", nil, fmt.Errorf("workload is required")
		}
		if err := validateNamespace(workload); err != nil {
			return "", nil, err
		}
		replicas, err := parseReplicas(v["replicas"], 1)
		if err != nil {
			return "", nil, err
		}
		if replicas < 1 {
			replicas = 1
		}
		target, err := m.poolTargetFrom(v["type"], v["namespace"], v["pool"], replicas)
		if err != nil {
			return "", nil, err
		}
		params := core.BurstParams{Target: target, Workload: workload, PoolNode: target.PoolType}
		prompt := fmt.Sprintf("Burst workload %q onto pool %s/%s (%d replicas)?",
			workload, target.Namespace, target.Name, target.Replicas)
		return prompt, m.runBurst(params), nil
	})
}

func (m model) backupCreateForm() formState {
	fields := []formField{
		{label: "include-namespaces", input: newTextInput("(all)", "")},
		{label: "exclude-namespaces", input: newTextInput("", "")},
		{label: "include-resources", input: newTextInput("", "")},
		{label: "selector", input: newTextInput("k=v,k2=v2", "")},
		{label: "storage-location", input: newTextInput(core.DefaultStorageLocation, core.DefaultStorageLocation)},
		{label: "ttl", input: newTextInput(core.DefaultBackupTTL.String(), core.DefaultBackupTTL.String())},
		{label: "snapshot-volumes", input: newTextInput("true", "true")},
		{label: "name", input: newTextInput("(auto)", "")},
		{label: "wait", input: newTextInput("false", "false")},
	}
	return newForm(actionBackupCreate, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		spec, err := buildBackupSpecFromValues(v)
		if err != nil {
			return "", nil, err
		}
		name := strings.TrimSpace(v["name"])
		if name == "" {
			name = core.DefaultBackupName(spec.IncludedNamespaces, time.Now())
		}
		wait := parseBool(v["wait"])
		prompt := fmt.Sprintf("Create backup %q (wait=%t)?", name, wait)
		return prompt, m.runBackupCreate(spec, name, wait), nil
	})
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

func (m model) restoreCreateForm(fromBackup string) formState {
	fields := []formField{
		{label: "include-namespaces", input: newTextInput("(all from backup)", "")},
		{label: "namespace-mappings", input: newTextInput("old:new,old2:new2", "")},
		{label: "name", input: newTextInput("(auto)", "")},
		{label: "wait", input: newTextInput("false", "false")},
	}
	f := newForm(actionRestoreCreate, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		spec, err := buildRestoreSpecFromValues(m.overlay.form.picked, v)
		if err != nil {
			return "", nil, err
		}
		name := strings.TrimSpace(v["name"])
		if name == "" {
			name = core.DefaultRestoreName(spec.BackupName, time.Now())
		}
		wait := parseBool(v["wait"])
		prompt := fmt.Sprintf("Restore from backup %q as %q (wait=%t)?", spec.BackupName, name, wait)
		return prompt, m.runRestoreCreate(spec, name, wait), nil
	})
	f.picked = fromBackup
	return f
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

func (m model) clusterCreateForm() formState {
	cfg := m.app.Config
	fields := []formField{
		{label: "name", input: newTextInput("cluster name (required)", "")},
		{label: "namespace", input: newTextInput(cfg.Pools.Namespace, "")},
		{label: "version", input: newTextInput(cfg.Pools.Version, "")},
		{label: "pod-cidr", input: newTextInput(defaultPodCIDR, defaultPodCIDR)},
		{label: "service-cidr", input: newTextInput(defaultServiceCIDR, defaultServiceCIDR)},
		{label: "replicas", input: newTextInput("1", "1")},
	}
	return newForm(actionClusterCreate, fields, func(m model, v map[string]string) (string, tea.Cmd, error) {
		spec, err := clusterSpecFromValues(m, v)
		if err != nil {
			return "", nil, err
		}
		return "", m.renderClusterPreview(spec), nil
	})
}

func clusterSpecFromValues(m model, v map[string]string) (capi.ClusterSpec, error) {
	name := strings.TrimSpace(v["name"])
	if name == "" {
		return capi.ClusterSpec{}, fmt.Errorf("name is required")
	}
	namespace := orDefault(strings.TrimSpace(v["namespace"]), m.app.Config.Pools.Namespace)
	version := orDefault(strings.TrimSpace(v["version"]), m.app.Config.Pools.Version)
	if version == "" {
		return capi.ClusterSpec{}, fmt.Errorf("version is required")
	}
	replicas, err := parseReplicas(v["replicas"], 1)
	if err != nil {
		return capi.ClusterSpec{}, err
	}
	return capi.ClusterSpec{
		Name:             name,
		Namespace:        namespace,
		ClusterName:      name,
		ControlPlaneMode: capi.Managed,
		PodCIDR:          orDefault(strings.TrimSpace(v["pod-cidr"]), defaultPodCIDR),
		ServiceCIDR:      orDefault(strings.TrimSpace(v["service-cidr"]), defaultServiceCIDR),
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
