package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

func TestRootCommandPrintsHelp(t *testing.T) {
	cmd := cli.NewRootCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("root output = %q, want the usage text", out.String())
	}
}
