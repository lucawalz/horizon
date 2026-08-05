package hetzner

import (
	"fmt"
	"strings"

	"github.com/lucawalz/horizon/internal/provider"
)

const (
	sentinelPrefix = provider.SentinelPrefix
	sentinelSuffix = provider.SentinelSuffix
)

const (
	NodeTokenSentinel   = provider.NodeTokenSentinel
	VersionSentinel     = provider.VersionSentinel
	MaxLifetimeSentinel = provider.MaxLifetimeSentinel
	JoinTokenSentinel   = provider.JoinTokenSentinel
)

func RenderUserData(template string, values map[string]string) (string, error) {
	pairs := make([]string, 0, 2*len(values))
	for sentinel, value := range values {
		pairs = append(pairs, sentinel, value)
	}
	rendered := strings.NewReplacer(pairs...).Replace(template)
	if unresolved, found := unresolvedSentinel(rendered); found {
		return "", fmt.Errorf("hetzner: cloud-init leaves %s unresolved", unresolved)
	}
	return rendered, nil
}

func unresolvedSentinel(rendered string) (string, bool) {
	for _, sentinel := range provider.Sentinels() {
		if strings.Contains(rendered, sentinel) {
			return sentinel, true
		}
	}
	start := strings.Index(rendered, sentinelPrefix)
	if start < 0 {
		return "", false
	}
	end := strings.Index(rendered[start:], sentinelSuffix)
	if end < 0 {
		return sentinelPrefix, true
	}
	return rendered[start : start+end+len(sentinelSuffix)], true
}

func buildUserData(userData string) (string, error) {
	if strings.TrimSpace(userData) == "" {
		return "", fmt.Errorf("hetzner: cloud-init is empty")
	}
	label := provider.PoolLabelKey + "=" + provider.ReservedPoolValue
	if !strings.Contains(userData, label) {
		return "", fmt.Errorf("hetzner: cloud-init missing node-label %q", label)
	}
	return userData, nil
}
