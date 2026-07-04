package hcloud

import (
	"fmt"
	"strings"

	"github.com/lucawalz/horizon/internal/provider"
)

func buildUserData(userData string) (string, error) {
	if strings.TrimSpace(userData) == "" {
		return "", fmt.Errorf("hcloud: cloud-init is empty")
	}
	label := provider.PoolLabelKey + "=" + provider.ReservedPoolValue
	if !strings.Contains(userData, label) {
		return "", fmt.Errorf("hcloud: cloud-init missing node-label %q", label)
	}
	return userData, nil
}
