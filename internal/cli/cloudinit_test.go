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

func TestCloudInitCommandWriteFileContentKeepsColons(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--passthrough",
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

func TestCloudInitCommandDefaultsArchitectureToAMD64(t *testing.T) {
	out, err := runCloudInit(t, []string{"--server", "https://10.20.0.10:6443"})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "linux_amd64") {
		t.Fatal("an unset --arch must default to amd64")
	}
}

func TestCloudInitCommandAcceptsAnArchitecture(t *testing.T) {
	out, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--arch", "arm64",
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !strings.Contains(out, "linux_arm64") {
		t.Fatal("--arch arm64 must select the arm64 binary")
	}
}

func TestCloudInitCommandRejectsAnUnknownArchitecture(t *testing.T) {
	if _, err := runCloudInit(t, []string{
		"--server", "https://10.20.0.10:6443",
		"--arch", "mips",
	}); err == nil {
		t.Fatal("expected an error for an unknown architecture")
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
