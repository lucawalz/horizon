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
		"up --type elastic 3",
		"up --type reserved --replicas 3",
		"burst myns",
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
		{"down", "delete all reserved servers"},
		{"down --delete", "delete all reserved servers"},
		{"drain worker-1", "drain node"},
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
		"drain",
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

func TestDispatchStripsDebugFlag(t *testing.T) {
	m := testModel()
	res := m.dispatch("down --debug")
	if len(res.lines) != 0 {
		t.Fatalf("down --debug unexpected error: %v", res.lines)
	}
	if res.cmd == nil {
		t.Error("down --debug expected a cmd")
	}

	if _, debug := stripDebugFlag([]string{"--type", "elastic", "--debug", "3"}); !debug {
		t.Error("expected debug true when --debug present")
	}
	got, debug := stripDebugFlag([]string{"--type", "elastic", "3"})
	if debug {
		t.Error("expected debug false when --debug absent")
	}
	if strings.Join(got, " ") != "--type elastic 3" {
		t.Errorf("stripped args = %q", got)
	}
	stripped, _ := stripDebugFlag([]string{"--debug", "--type", "elastic"})
	for _, a := range stripped {
		if a == "--debug" {
			t.Error("--debug should be removed from args")
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

func TestResolveUpReplicas(t *testing.T) {
	cases := []struct {
		name       string
		flag       int
		positional []string
		want       int32
	}{
		{"flag set", 3, nil, 3},
		{"positional only", 0, []string{"2"}, 2},
		{"bare default", 0, nil, 1},
		{"flag beats positional", 3, []string{"2"}, 3},
	}
	for _, tc := range cases {
		got, err := resolveUpReplicas(tc.flag, tc.positional)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: resolveUpReplicas(%d, %v) = %d, want %d", tc.name, tc.flag, tc.positional, got, tc.want)
		}
	}
	if _, err := resolveUpReplicas(0, []string{"notanumber"}); err == nil {
		t.Error("expected error for non-numeric positional replicas")
	}
}
