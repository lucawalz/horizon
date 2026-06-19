package hcloud

import (
	"fmt"
	"strings"
)

type UserDataInput struct {
	ElasticCloudInit string
	ElasticPoolValue string
}

func BuildUserData(in UserDataInput) (string, error) {
	if strings.TrimSpace(in.ElasticCloudInit) == "" {
		return "", fmt.Errorf("hcloud: elastic cloud-init template is empty")
	}
	from := PoolLabelKey + "=" + in.ElasticPoolValue
	to := PoolLabelKey + "=" + ReservedPoolValue
	if !strings.Contains(in.ElasticCloudInit, from) {
		return "", fmt.Errorf("hcloud: cloud-init template missing node-label %q", from)
	}
	return strings.ReplaceAll(in.ElasticCloudInit, from, to), nil
}
