package hetzner

import (
	"testing"
)

const reservedCloudInit = `#cloud-config
write_files:
  - path: /etc/rancher/k3s/config.yaml
    content: |
      server: https://10.20.0.10:6443
      token: K10secret::server:abc
      node-label:
      - horizon.dev/pool=reserved
`

func TestBuildUserDataReturnsValidTemplateUnchanged(t *testing.T) {
	out, err := buildUserData(reservedCloudInit)
	if err != nil {
		t.Fatalf("buildUserData: %v", err)
	}
	if out != reservedCloudInit {
		t.Errorf("cloud-init must be returned unchanged:\n%s", out)
	}
}

func TestBuildUserDataFailsFastOnEmptyTemplate(t *testing.T) {
	if _, err := buildUserData("  \n"); err == nil {
		t.Fatal("expected error on empty cloud-init")
	}
}

func TestBuildUserDataFailsFastWhenReservedLabelMissing(t *testing.T) {
	if _, err := buildUserData("#cloud-config\nnode-label:\n- horizon.dev/pool=elastic\n"); err == nil {
		t.Fatal("expected error when reserved node-label is absent")
	}
}
