package hcloud

import (
	"fmt"
	"strings"
)

func BuildUserData(userData string) (string, error) {
	if strings.TrimSpace(userData) == "" {
		return "", fmt.Errorf("hcloud: cloud-init is empty")
	}
	label := PoolLabelKey + "=" + ReservedPoolValue
	if !strings.Contains(userData, label) {
		return "", fmt.Errorf("hcloud: cloud-init missing node-label %q", label)
	}
	return userData, nil
}
