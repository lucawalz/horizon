package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lucawalz/horizon/internal/core"
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
