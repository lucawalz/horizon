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
			ClusterCreate: config.ClusterDefaults{
				Class:       "hetzner-k3s",
				WorkerClass: "default-worker",
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
		"schedule list",
		"schedule create nightly --schedule @daily --include-namespaces app",
		"schedule describe nightly",
		"bsl list",
		"bsl create secondary --provider aws --bucket horizon-backups",
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
		{"schedule delete nightly", "delete schedule"},
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
		"schedule",
		"schedule create nightly",
		"schedule create --schedule @daily",
		"schedule describe",
		"schedule delete",
		"schedule bogus",
		"bsl",
		"bsl create secondary",
		"bsl create secondary --provider aws",
		"bsl create --provider aws --bucket b",
		"bsl bogus",
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

func TestClusterCreateRequiresClassOrFlavor(t *testing.T) {
	m := testModel()
	m.app.Config.ClusterCreate.Class = ""
	res := m.dispatch("cluster create demo")
	if len(res.lines) == 0 {
		t.Error("expected error when neither --class nor --flavor is given and no default class exists")
	}
}

func TestClusterCreateClassAndFlavorMutuallyExclusive(t *testing.T) {
	m := testModel()
	res := m.dispatch("cluster create demo --class hetzner-k3s --flavor /tmp/x.yaml")
	if len(res.lines) == 0 {
		t.Error("expected error when both --class and --flavor are given")
	}
}

func TestClusterCreateBuildsTopologySpec(t *testing.T) {
	m := testModel()
	spec, err := m.clusterSpecFrom(clusterCreateInput{
		name:                 "demo",
		class:                "hetzner-k3s",
		workerClass:          "default-worker",
		replicas:             3,
		controlPlaneReplicas: 1,
		sets:                 []string{"machineType=cpx22", "diskSize=40"},
	})
	if err != nil {
		t.Fatalf("clusterSpecFrom: %v", err)
	}
	if spec.Class != "hetzner-k3s" || spec.WorkerClass != "default-worker" {
		t.Errorf("spec class fields = %+v", spec)
	}
	if spec.WorkerReplicas != 3 || spec.ControlPlaneReplicas != 1 {
		t.Errorf("spec replicas = cp %d worker %d", spec.ControlPlaneReplicas, spec.WorkerReplicas)
	}
	if len(spec.Variables) != 2 || spec.Variables[0].Name != "machineType" || spec.Variables[0].Value != "cpx22" {
		t.Errorf("spec variables = %+v", spec.Variables)
	}
}

func TestParseSetVarsRejectsMalformed(t *testing.T) {
	if _, err := parseSetVars([]string{"noequals"}); err == nil {
		t.Error("expected error for --set without =")
	}
	if _, err := parseSetVars([]string{"=value"}); err == nil {
		t.Error("expected error for --set with empty key")
	}
}

func TestClusterCreateFlavorMissingFileErrors(t *testing.T) {
	m := testModel()
	res := m.dispatch("cluster create demo --flavor /no/such/flavor.yaml")
	if len(res.lines) == 0 {
		t.Error("expected error when flavor file cannot be read")
	}
}

func TestClusterCreateWriteRequiresRepoPath(t *testing.T) {
	m := testModel()
	res := m.dispatch("cluster create demo --write")
	if len(res.lines) == 0 {
		t.Error("expected error when repo_path is unset and --write is used")
	}
	m.app.Config.RepoPath = "/tmp/repo"
	res = m.dispatch("cluster create demo --write")
	if len(res.lines) != 0 || res.cmd == nil {
		t.Errorf("cluster create --write with repo_path should succeed, got %+v", res)
	}
}
