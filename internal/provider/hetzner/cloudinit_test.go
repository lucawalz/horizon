package hetzner

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cloudinit"
	"github.com/lucawalz/horizon/internal/provider"
)

func generatedCloudInit(t *testing.T) string {
	t.Helper()
	installWatchdogUnit := true
	blob, err := cloudinit.Render(cloudinit.Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   "v1.35.6+k3s1",
		Labels:              []string{provider.PoolLabelAssignment},
		InstallWatchdogUnit: &installWatchdogUnit,
	})
	if err != nil {
		t.Fatalf("cloudinit.Render: %v", err)
	}
	return blob
}

func generatedValues() map[string]string {
	values := make(map[string]string, len(provider.Sentinels()))
	for i, sentinel := range provider.Sentinels() {
		values[sentinel] = "supplied-" + strconv.Itoa(i)
	}
	return values
}

func TestGeneratedCloudInitPassesTheProviderBuild(t *testing.T) {
	blob := generatedCloudInit(t)
	for _, sentinel := range provider.Sentinels() {
		if !strings.Contains(blob, sentinel) {
			t.Errorf("the generated cloud-init never uses %s, so no lease proves it has a supplier", sentinel)
		}
	}

	rendered, err := RenderUserData(blob, generatedValues())
	if err != nil {
		t.Fatalf("the generated cloud-init did not survive substitution: %v", err)
	}
	built, err := buildUserData(rendered)
	if err != nil {
		t.Fatalf("the generated cloud-init was refused by the provider build: %v", err)
	}
	if built != rendered {
		t.Error("the provider build rewrote the generated cloud-init")
	}
}

func TestGeneratedCloudInitIsRefusedWhenASupplierIsMissing(t *testing.T) {
	blob := generatedCloudInit(t)
	for _, missing := range provider.Sentinels() {
		t.Run(missing, func(t *testing.T) {
			values := generatedValues()
			delete(values, missing)
			rendered, err := RenderUserData(blob, values)
			if err == nil {
				t.Fatalf("a cloud-init with no value for %s reached the provider build:\n%s", missing, rendered)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error is %q, want it to name %s", err, missing)
			}
		})
	}
}
