package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDownDryRun(t *testing.T) {
	app := newTestApp()
	out := captureStdout(func() {
		cli.RunDownDryRunForTest(app)
	})
	for i := 1; i <= 4; i++ {
		want := fmt.Sprintf("[dry-run] Step %d:", i)
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Remove burst node WireGuard peer from hub") {
		t.Errorf("dry-run output missing wg-peer-remove label:\n%s", out)
	}
	if !strings.Contains(out, "[dry-run] No actions executed.") {
		t.Errorf("missing trailing line:\n%s", out)
	}
}

func seededState(t *testing.T, stateDir string, st cli.BurstState) {
	t.Helper()
	if err := cli.WriteState(stateDir, st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
}

func TestDownStepOrder(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:         "aabb1122",
		Hostname:        "horizon-burst-aabb1122",
		WireGuardPubKey: "member-7",
		HetznerServerID: "42",
	}
	seededState(t, stateDir, st)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "aabb1122", hostname: "horizon-burst-aabb1122"}
	kc := fake.NewSimpleClientset()

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	if prov.destroyCalls != 1 {
		t.Errorf("Destroy calls = %d, want 1", prov.destroyCalls)
	}
	if len(pm.removeCalls) != 1 || pm.removeCalls[0] != "member-7" {
		t.Errorf("RemovePeer calls = %v, want [member-7]", pm.removeCalls)
	}
	if _, err := cli.ReadState(stateDir, "aabb1122"); err == nil {
		t.Error("state file still exists after down")
	}
}

type recordingHetznerProvider struct {
	mockHetznerProvider
	events *[]string
}

func (m *recordingHetznerProvider) Destroy(ctx context.Context) error {
	*m.events = append(*m.events, "destroy")
	return m.mockHetznerProvider.Destroy(ctx)
}

func TestDownDestroysVMBeforeDeletingNode(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-cc33dd44"
	st := cli.BurstState{
		BurstID:         "cc33dd44",
		Hostname:        hostname,
		WireGuardPubKey: "member-9",
		HetznerServerID: "77",
	}
	seededState(t, stateDir, st)

	var events []string
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: hostname}}
	kc := fake.NewSimpleClientset(node)
	kc.PrependReactor("delete", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		events = append(events, "delete-node")
		return false, nil, nil
	})

	pm := &mockPeerManager{}
	prov := &recordingHetznerProvider{
		mockHetznerProvider: mockHetznerProvider{burstID: "cc33dd44", hostname: hostname},
		events:              &events,
	}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	destroyIdx, deleteIdx := -1, -1
	for i, e := range events {
		switch e {
		case "destroy":
			destroyIdx = i
		case "delete-node":
			deleteIdx = i
		}
	}
	if destroyIdx == -1 {
		t.Fatalf("destroy was not recorded: %v", events)
	}
	if deleteIdx == -1 {
		t.Fatalf("delete-node was not recorded: %v", events)
	}
	if destroyIdx >= deleteIdx {
		t.Errorf("destroy must run before delete-node: %v", events)
	}
}

func TestDownContinuesOnEmptyMemberID(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:         "eeff5566",
		Hostname:        "horizon-burst-eeff5566",
		WireGuardPubKey: "",
		HetznerServerID: "20",
	}
	seededState(t, stateDir, st)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "eeff5566", hostname: "horizon-burst-eeff5566"}
	kc := fake.NewSimpleClientset()

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("expected success when member id empty, got: %v", err)
	}
	if len(pm.removeCalls) != 0 {
		t.Errorf("RemovePeer called unexpectedly: %v", pm.removeCalls)
	}
	if prov.destroyCalls != 1 {
		t.Errorf("Destroy still must run, got %d", prov.destroyCalls)
	}
}

func TestDownNoBurstIDFlagSelectsSingleState(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:         "ffff7777",
		Hostname:        "horizon-burst-ffff7777",
		WireGuardPubKey: "m5",
		HetznerServerID: "30",
	}
	seededState(t, stateDir, st)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "ffff7777", hostname: "horizon-burst-ffff7777"}
	kc := fake.NewSimpleClientset()

	resolved, err := cli.ResolveBurstIDForTest(stateDir, "")
	if err != nil {
		t.Fatalf("ResolveBurstID: %v", err)
	}
	if resolved != "ffff7777" {
		t.Errorf("resolved = %q, want ffff7777", resolved)
	}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}
}

func TestDownNoBurstIDFlagAmbiguous(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	for _, id := range []string{"aaaa1111", "bbbb2222"} {
		seededState(t, stateDir, cli.BurstState{BurstID: id})
	}

	_, err := cli.ResolveBurstIDForTest(stateDir, "")
	if err == nil {
		t.Fatal("expected error for multiple state files")
	}
	if !strings.Contains(err.Error(), "burst-id") {
		t.Errorf("error %q should contain 'burst-id'", err.Error())
	}
	if !strings.Contains(err.Error(), "aaaa1111") || !strings.Contains(err.Error(), "bbbb2222") {
		t.Errorf("error %q should list available ids", err.Error())
	}

	emptyDir := t.TempDir()
	_, zeroErr := cli.ResolveBurstIDForTest(emptyDir, "")
	if zeroErr == nil || !strings.Contains(zeroErr.Error(), "burst-id") {
		t.Errorf("zero-state error = %v", zeroErr)
	}
}

func TestDownEvictsNonDaemonSetPods(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb9999"
	st := cli.BurstState{
		BurstID:         "aabb9999",
		Hostname:        hostname,
		WireGuardPubKey: "m3",
		HetznerServerID: "50",
	}
	seededState(t, stateDir, st)

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: hostname}}
	deployPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "deploy-pod",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs-1"}},
		},
		Spec: corev1.PodSpec{NodeName: hostname},
	}
	dsPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "ds-pod",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "ds-1"}},
		},
		Spec: corev1.PodSpec{NodeName: hostname},
	}
	kc := fake.NewSimpleClientset(node, deployPod, dsPod)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "aabb9999", hostname: hostname}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	var evictActions []k8stesting.Action
	for _, a := range kc.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			evictActions = append(evictActions, a)
		}
	}
	if len(evictActions) != 1 {
		t.Errorf("eviction count = %d, want 1 (only deploy-pod, not ds-pod)", len(evictActions))
	}

	if _, statErr := os.Stat(stateDir + "/aabb9999.json"); !os.IsNotExist(statErr) {
		t.Error("state file should be deleted after successful down")
	}
}

func TestDownStatelessDerivesHostname(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	burstID := "ab12cd34"
	hostname := "horizon-burst-" + burstID
	st := cli.BurstState{BurstID: burstID, Hostname: hostname}

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: hostname}}
	kc := fake.NewSimpleClientset(node)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: burstID, hostname: hostname}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}
	if prov.destroyCalls != 1 {
		t.Errorf("Destroy calls = %d, want 1", prov.destroyCalls)
	}
	if len(pm.removeCalls) != 0 {
		t.Errorf("peer removal must be skipped when pubkey unknown: %v", pm.removeCalls)
	}
	if _, err := kc.CoreV1().Nodes().Get(context.Background(), hostname, metav1.GetOptions{}); err == nil {
		t.Error("burst node should be deleted")
	}
}

func TestDownUnpinsWorkloadOnLastBurstNode(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	burstID := "ba98fe76"
	hostname := "horizon-burst-" + burstID
	ns := "sentio-systems"
	st := cli.BurstState{BurstID: burstID, Hostname: hostname}
	seededState(t, stateDir, st)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   hostname,
			Labels: map[string]string{k8s.PoolLabelKey: ns},
		},
	}
	pinned := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      k8s.PoolLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{ns},
					}},
				}},
			},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Affinity: pinned}},
		},
	}
	kc := fake.NewSimpleClientset(node, dep)

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: burstID, hostname: hostname}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	got, err := kc.AppsV1().Deployments(ns).Get(context.Background(), "app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if a := got.Spec.Template.Spec.Affinity; a != nil && a.NodeAffinity != nil && a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		t.Errorf("workload still pinned after last burst node removed: %+v", a.NodeAffinity)
	}
}

func TestDownPrunesBurstPhase(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:         "aabb5555",
		Hostname:        "horizon-burst-aabb5555",
		WireGuardPubKey: "m9",
		HetznerServerID: "60",
	}
	seededState(t, stateDir, st)

	kc := fake.NewSimpleClientset()
	if err := k8s.WriteBurstPhase(context.Background(), kc, "aabb5555", k8s.BurstPhaseTearingDown); err != nil {
		t.Fatalf("seed burst phase: %v", err)
	}
	if err := k8s.WriteBurstPhase(context.Background(), kc, "ccdd6666", k8s.BurstPhaseRunning); err != nil {
		t.Fatalf("seed sibling phase: %v", err)
	}

	pm := &mockPeerManager{}
	prov := &mockHetznerProvider{burstID: "aabb5555", hostname: "horizon-burst-aabb5555"}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), pm, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	phases, err := k8s.ReadBurstPhases(context.Background(), kc)
	if err != nil {
		t.Fatalf("ReadBurstPhases: %v", err)
	}
	if _, ok := phases["aabb5555"]; ok {
		t.Errorf("torn-down burst should be pruned, got %v", phases)
	}
	if phases["ccdd6666"] != k8s.BurstPhaseRunning {
		t.Errorf("sibling burst phase must survive, got %q", phases["ccdd6666"])
	}
}
