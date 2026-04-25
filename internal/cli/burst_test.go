package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
)

func TestDryRun(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	app := &cli.App{
		Config: &config.Config{Provider: "hetzner"},
	}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{"--dry-run"})
	_ = cmd.Execute()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	steps := []string{
		"[dry-run] Step 1:",
		"[dry-run] Step 2:",
		"[dry-run] Step 3:",
		"[dry-run] Step 4:",
		"[dry-run] Step 5:",
		"[dry-run] Step 6:",
		"[dry-run] No actions executed.",
	}
	for _, step := range steps {
		if !strings.Contains(out, step) {
			t.Errorf("expected output to contain %q\ngot:\n%s", step, out)
		}
	}
}

func TestBurstDryRunNoCloudCreds(t *testing.T) {
	origToken := os.Getenv("HCLOUD_TOKEN")
	os.Unsetenv("HCLOUD_TOKEN")
	defer os.Setenv("HCLOUD_TOKEN", origToken)

	app := &cli.App{
		Config: &config.Config{Provider: "hetzner"},
	}
	cmd := cli.NewBurstCmdForTest(app)
	cmd.SetArgs([]string{"--dry-run"})

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := cmd.Execute()
	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Errorf("burst --dry-run must succeed without HCLOUD_TOKEN; got: %v", err)
	}
}
