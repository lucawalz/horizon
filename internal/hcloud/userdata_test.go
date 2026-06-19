package hcloud_test

import (
	"strings"
	"testing"

	hz "github.com/lucawalz/horizon/internal/hcloud"
)

const elasticCloudInit = `#cloud-config
write_files:
  - path: /etc/rancher/k3s/config.yaml
    content: |
      server: https://10.20.0.10:6443
      token: K10secret::server:abc
      node-label:
      - horizon.dev/pool=elastic
  - path: /etc/tailscale/authkey
    content: |
      tskey-auth-xyz
`

func TestBuildUserDataRewritesPoolLabel(t *testing.T) {
	out, err := hz.BuildUserData(hz.UserDataInput{ElasticCloudInit: elasticCloudInit, ElasticPoolValue: "elastic"})
	if err != nil {
		t.Fatalf("BuildUserData: %v", err)
	}
	if !strings.Contains(out, "horizon.dev/pool=reserved") {
		t.Errorf("user-data must carry reserved node-label:\n%s", out)
	}
	if strings.Contains(out, "horizon.dev/pool=elastic") {
		t.Errorf("user-data must not retain elastic node-label:\n%s", out)
	}
	if !strings.Contains(out, "server: https://10.20.0.10:6443") {
		t.Errorf("user-data must preserve the join server URL:\n%s", out)
	}
	if !strings.Contains(out, "tskey-auth-xyz") {
		t.Errorf("user-data must preserve the tailscale authkey:\n%s", out)
	}
}

func TestBuildUserDataFailsFastOnEmptyTemplate(t *testing.T) {
	if _, err := hz.BuildUserData(hz.UserDataInput{ElasticPoolValue: "elastic"}); err == nil {
		t.Fatal("expected error on empty cloud-init")
	}
}

func TestBuildUserDataFailsFastWhenLabelMissing(t *testing.T) {
	if _, err := hz.BuildUserData(hz.UserDataInput{ElasticCloudInit: "#cloud-config\n", ElasticPoolValue: "elastic"}); err == nil {
		t.Fatal("expected error when node-label not present in template")
	}
}
