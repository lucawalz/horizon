package cli_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

func TestInitCommandBypassesConfigLoad(t *testing.T) {
	cmd := cli.NewInitCmdForTest()
	if cmd.PersistentPreRunE == nil {
		t.Fatal("init command must define a no-op PersistentPreRunE")
	}
	if err := cmd.PersistentPreRunE(cmd, nil); err != nil {
		t.Errorf("PersistentPreRunE returned %v, want nil", err)
	}
}
