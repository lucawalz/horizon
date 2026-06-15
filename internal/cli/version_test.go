package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/version"
)

func TestVersionCommandPrintsBuildVersion(t *testing.T) {
	cmd := cli.NewVersionCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != version.Version() {
		t.Errorf("version output = %q, want %q", got, version.Version())
	}
}
