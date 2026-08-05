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

func boolPtr(b bool) *bool { return &b }

func TestRenderK3sIncludesTheContract(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
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
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "get.k3s.io") {
		t.Fatalf("expected the flavour install command, got:\n%s", out)
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
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "permissions: '0600'") {
		t.Fatal("permissions must be a quoted octal string")
	}
}

func TestRenderOmitsWatchdogUnitWhenDisabled(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", InstallWatchdogUnit: boolPtr(false)})
	if strings.Contains(out, "systemctl enable") {
		t.Fatal("no unit should be installed when the flag is false")
	}
	if !strings.Contains(out, "${HORIZON_NODE_TOKEN}") {
		t.Fatal("the node token file is still required")
	}
}

func TestRenderUsesConfiguredBinaryBaseURL(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", BinaryBaseURL: "https://mirror.internal/horizon", InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "https://mirror.internal/horizon") {
		t.Fatal("the configured base URL must be used")
	}
	if strings.Contains(out, "github.com/lucawalz/horizon/releases") {
		t.Fatal("the default must not leak through when overridden")
	}
}

func TestRenderResolvesTheArchitectureOnTheNode(t *testing.T) {
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", InstallWatchdogUnit: boolPtr(true)})
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
	out, _ := Render(Options{Flavor: "k3s", Server: "https://x:6443", InstallWatchdogUnit: boolPtr(true)})
	if !strings.Contains(out, "/var/lib/horizon/bin/horizon version") {
		t.Fatal("the install script must probe the installed binary")
	}
}

func TestRenderProducesAParsableDocument(t *testing.T) {
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
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
	out, err := Render(Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		Labels:              []string{"horizon.dev/pool=reserved"},
		Taints:              []string{"example.dev/dedicated=batch:NoSchedule"},
		InstallWatchdogUnit: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	golden := filepath.Join("testdata", "k3s-default.golden")
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
}
