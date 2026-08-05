package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

func runCloudInit(t *testing.T, args []string) (string, error) {
	t.Helper()
	cmd := cli.NewCloudInitCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCloudInitCommandWritesToStdout(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--label", "horizon.dev/pool=reserved",
		"--taint", "horizon.dev/burst=true:NoSchedule",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.HasPrefix(out, "#cloud-config") {
		t.Fatalf("output does not look like cloud-config: %q", out[:32])
	}
}

func TestCloudInitCommandRejectsMissingServer(t *testing.T) {
	if _, err := runCloudInit(t, nil); err == nil {
		t.Fatal("expected an error when no server is given")
	}
}

func TestCloudInitCommandAddsThePoolLabelByDefault(t *testing.T) {
	out, err := runCloudInit(t, []string{"--server", "https://10.20.0.10:6443"})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "horizon.dev/pool=reserved") {
		t.Fatal("the reserved pool label must be present without any --label flag")
	}
}

func TestCloudInitCommandKeepsThePoolLabelAlongsideOtherLabels(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--label", "team=platform",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "team=platform") {
		t.Fatal("a caller-supplied label must survive")
	}
	if !strings.Contains(out, "horizon.dev/pool=reserved") {
		t.Fatal("the reserved pool label must not be lost when other labels are given")
	}
}

func TestCloudInitCommandDoesNotDuplicateAnExplicitPoolLabel(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--label", "horizon.dev/pool=reserved",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Count(out, "horizon.dev/pool=reserved") != 1 {
		t.Fatalf("the pool label must appear exactly once, got:\n%s", out)
	}
}

func TestCloudInitCommandRejectsAConflictingPoolLabel(t *testing.T) {
	_, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--label", "horizon.dev/pool=spot",
	})
	if err == nil {
		t.Fatal("expected an error when the pool label carries a conflicting value")
	}
	if !strings.Contains(err.Error(), "horizon.dev/pool") || !strings.Contains(err.Error(), "spot") {
		t.Errorf("error = %q, want it to name the key and the conflicting value", err)
	}
}

func TestCloudInitCommandRejectsMalformedWriteFile(t *testing.T) {
	_, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--write-file", "onlyonefield",
	})
	if err == nil {
		t.Fatal("expected an error for a malformed --write-file spec")
	}
	if !strings.Contains(err.Error(), "--write-file") {
		t.Errorf("error = %q, want it to name the flag", err)
	}
}

const passthroughPoolFile = "/etc/caller/join.yaml:0600:node-label: horizon.dev/pool=reserved"

func TestCloudInitCommandWriteFileContentKeepsColons(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--passthrough",
		"--write-file", passthroughPoolFile,
		"--write-file", "/etc/hosts:0644:10.20.0.10:6443 server.local",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "path: /etc/hosts") {
		t.Fatalf("path did not parse correctly, got:\n%s", out)
	}
	if !strings.Contains(out, "permissions: '0644'") {
		t.Fatalf("permissions did not parse correctly, got:\n%s", out)
	}
	if !strings.Contains(out, "10.20.0.10:6443 server.local") {
		t.Fatalf("content with colons did not survive intact, got:\n%s", out)
	}
}

func TestCloudInitCommandKeepsCommasInContentAndCommands(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--write-file", "/etc/horizon/list:0644:alpha,beta",
		"--post-command", "echo alpha,beta",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "alpha,beta") {
		t.Fatalf("a comma in file content was split, got:\n%s", out)
	}
	if !strings.Contains(out, "echo alpha,beta") {
		t.Fatalf("a comma in a command was split, got:\n%s", out)
	}
	if strings.Count(out, "alpha,beta") != 2 {
		t.Fatalf("both the file and the command must survive intact, got:\n%s", out)
	}
}

func TestCloudInitCommandInstallWatchdogUnitDefaultsToTrue(t *testing.T) {
	flag := cli.NewCloudInitCmdForTest().Flags().Lookup("install-watchdog-unit")
	if flag == nil {
		t.Fatal("flag --install-watchdog-unit is not registered")
	}
	if flag.DefValue != "true" {
		t.Errorf("flag --install-watchdog-unit default = %q, want %q", flag.DefValue, "true")
	}
}

func TestCloudInitCommandCanDisableTheWatchdogUnit(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--install-watchdog-unit=false",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Contains(out, "systemctl enable") {
		t.Fatal("no watchdog unit should be installed when the flag is false")
	}
}

func TestCloudInitCommandCarriesNoArchitectureFlag(t *testing.T) {
	if flag := cli.NewCloudInitCmdForTest().Flags().Lookup("arch"); flag != nil {
		t.Error("the rendered document resolves the architecture on the node, so no --arch flag belongs")
	}
}

func TestCloudInitCommandPinsNoArchitecture(t *testing.T) {
	out, err := runCloudInit(t, []string{"--server", "https://10.20.0.10:6443"})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "ARCH=$(uname -m)") {
		t.Fatal("the rendered document must resolve the architecture on the node")
	}
}

func TestCloudInitCommandPassthroughRejectsFlagsItWouldDiscard(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "a label", args: []string{"--label", "team=platform"}, want: "--label"},
		{name: "a taint", args: []string{"--taint", "team=platform:NoSchedule"}, want: "--taint"},
		{name: "a server", args: []string{"--server", "https://10.20.0.10:6443"}, want: "--server"},
		{name: "a flavour", args: []string{"--flavor", "k3s"}, want: "--flavor"},
		{name: "the watchdog unit", args: []string{"--install-watchdog-unit=false"}, want: "--install-watchdog-unit"},
		{name: "a binary base URL", args: []string{"--binary-base-url", "https://mirror.internal"}, want: "--binary-base-url"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--passthrough", "--write-file", passthroughPoolFile}, tc.args...)
			out, err := runCloudInit(t, args)
			if err == nil {
				t.Fatalf("the flag was silently discarded, got:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error is %q, want it to name %s", err, tc.want)
			}
		})
	}
}

func TestCloudInitCommandPassthroughRequiresThePoolLabel(t *testing.T) {
	_, err := runCloudInit(t, []string{"--passthrough", "--write-file", "/etc/motd:0644:hello"})
	if err == nil {
		t.Fatal("expected an error when passthrough output carries no pool label")
	}
	if !strings.Contains(err.Error(), "horizon.dev/pool=reserved") {
		t.Errorf("error is %q, want it to name the required label", err)
	}
}

func TestCloudInitCommandPassthroughEmitsOnlyWhatTheCallerGave(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--passthrough",
		"--write-file", passthroughPoolFile,
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	for _, unwanted := range []string{"/etc/rancher", "get.k3s.io", "/etc/horizon/token", "horizon-watchdog.service"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("passthrough emitted %q, got:\n%s", unwanted, out)
		}
	}
}

func TestCloudInitCommandIsReachableFromRoot(t *testing.T) {
	for _, sub := range cli.NewRootCmdForTest().Commands() {
		if sub.Name() == "cloud-init" {
			return
		}
	}
	t.Error("root command has no cloud-init subcommand")
}
