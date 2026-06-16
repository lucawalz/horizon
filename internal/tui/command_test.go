package tui

import (
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
)

func testModel() model {
	return model{app: &core.App{
		Cluster: "edge",
		Config: &config.Config{
			Pools: config.PoolDefaults{
				Namespace:   "caph-system",
				DefaultType: "reserved",
				Version:     "v1.35.2+k3s1",
				Types: map[string]string{
					"reserved": "reserved-workers",
					"elastic":  "elastic-workers",
				},
			},
		},
	}}
}

func TestDispatchBuiltins(t *testing.T) {
	m := testModel()
	cases := map[string]builtinKind{
		"help":    builtinHelp,
		"refresh": builtinRefresh,
		"clear":   builtinClear,
		"quit":    builtinQuit,
		"exit":    builtinQuit,
	}
	for input, want := range cases {
		if got := m.dispatch(input).builtin; got != want {
			t.Errorf("dispatch(%q).builtin = %v, want %v", input, got, want)
		}
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	res := testModel().dispatch("frobnicate now")
	if len(res.lines) == 0 {
		t.Fatal("expected an error line for unknown command")
	}
	if res.cmd != nil || res.builtin != builtinNone {
		t.Errorf("unknown command should not produce a cmd or builtin")
	}
}

func TestDispatchEmptyInput(t *testing.T) {
	res := testModel().dispatch("   ")
	if res.cmd != nil || res.builtin != builtinNone || len(res.lines) != 0 {
		t.Errorf("empty input should be a no-op, got %+v", res)
	}
}

func TestDispatchNonDestructiveHaveNoConfirm(t *testing.T) {
	m := testModel()
	for _, input := range []string{
		"up",
		"up --type elastic --nudge 3",
		"down",
		"nudge",
		"burst myns",
		"cluster list",
		"cluster create demo",
		"backup list",
		"backup create --wait",
		"backup describe foo",
		"restore list",
		"restore describe r1",
	} {
		res := m.dispatch(input)
		if len(res.lines) != 0 {
			t.Errorf("dispatch(%q) unexpected error: %v", input, res.lines)
			continue
		}
		if res.cmd == nil {
			t.Errorf("dispatch(%q) expected a cmd", input)
		}
		if res.confirm != "" {
			t.Errorf("dispatch(%q) should not require confirm, got %q", input, res.confirm)
		}
	}
}

func TestDispatchDestructiveRequireConfirm(t *testing.T) {
	m := testModel()
	cases := []struct {
		input  string
		needle string
	}{
		{"down --delete", "delete pool"},
		{"cluster delete demo", "delete cluster"},
		{"backup delete b1", "delete backup"},
		{"drain worker-1", "drain node"},
		{"restore create --from-backup b1", "restore from backup"},
	}
	for _, tc := range cases {
		res := m.dispatch(tc.input)
		if len(res.lines) != 0 {
			t.Errorf("dispatch(%q) unexpected error: %v", tc.input, res.lines)
			continue
		}
		if res.cmd == nil {
			t.Errorf("dispatch(%q) expected a pending cmd", tc.input)
		}
		if !strings.Contains(res.confirm, tc.needle) {
			t.Errorf("dispatch(%q).confirm = %q, want it to contain %q", tc.input, res.confirm, tc.needle)
		}
	}
}

func TestDispatchMissingRequiredArgs(t *testing.T) {
	m := testModel()
	for _, input := range []string{
		"burst",
		"cluster create",
		"cluster delete",
		"backup describe",
		"backup delete",
		"restore create",
		"restore describe",
		"drain",
		"cluster bogus",
		"backup bogus",
		"restore bogus",
	} {
		res := m.dispatch(input)
		if len(res.lines) == 0 {
			t.Errorf("dispatch(%q) expected an error line", input)
		}
		if res.cmd != nil {
			t.Errorf("dispatch(%q) should not produce a cmd on error", input)
		}
	}
}

func TestUpParsesTypeAndReplicas(t *testing.T) {
	m := testModel()
	target, err := m.poolTargetFrom("elastic", "", "", 4)
	if err != nil {
		t.Fatalf("poolTargetFrom: %v", err)
	}
	if target.Name != "elastic-workers" || target.PoolType != "elastic" || target.Replicas != 4 {
		t.Errorf("poolTargetFrom = %+v", target)
	}
	if res := m.dispatch("up --type bogus"); len(res.lines) == 0 {
		t.Error("expected error for unknown pool type")
	}
	if res := m.dispatch("up notanumber"); len(res.lines) == 0 {
		t.Error("expected error for non-numeric replicas")
	}
}

func TestClusterCreateWriteRequiresBedrockPath(t *testing.T) {
	m := testModel()
	res := m.dispatch("cluster create demo --write")
	if len(res.lines) == 0 {
		t.Error("expected error when bedrock_path is unset and --write is used")
	}
	m.app.Config.BedrockPath = "/tmp/bedrock"
	res = m.dispatch("cluster create demo --write")
	if len(res.lines) != 0 || res.cmd == nil {
		t.Errorf("cluster create --write with bedrock_path should succeed, got %+v", res)
	}
}
