package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDownDryRun(t *testing.T) {
	app := newTestApp()
	out := captureStdout(func() {
		cli.RunDownDryRunForTest(app)
	})
	for i := 1; i <= 5; i++ {
		want := fmt.Sprintf("[dry-run] Step %d:", i)
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "[dry-run] No actions executed.") {
		t.Errorf("missing trailing line in output:\n%s", out)
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
		BurstID:             "aabb1122",
		Hostname:            "horizon-burst-aabb1122",
		HeadscaleNodeID:     "7",
		HeadscalePreAuthKey: "key-abc",
		HetznerServerID:     "42",
	}
	seededState(t, stateDir, st)

	hs := &mockHeadscaler{}
	prov := &mockHetznerProvider{burstID: "aabb1122", hostname: "horizon-burst-aabb1122"}
	kc := fake.NewSimpleClientset()

	if err := cli.RunDownForTest(context.Background(), newTestApp(), hs, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	if prov.destroyCalls != 1 {
		t.Errorf("Destroy calls = %d, want 1", prov.destroyCalls)
	}
	if len(hs.deleteCalls) != 1 || hs.deleteCalls[0] != "7" {
		t.Errorf("DeleteNode calls = %v, want [7]", hs.deleteCalls)
	}
	if len(hs.revokeCalls) != 1 || hs.revokeCalls[0] != "key-abc" {
		t.Errorf("RevokePreAuthKey calls = %v, want [key-abc]", hs.revokeCalls)
	}

	if _, err := cli.ReadState(stateDir, "aabb1122"); err == nil {
		t.Error("state file still exists after down")
	}
}

func TestDownDeletesNodeByPersistedHostname(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:             "ccdd3344",
		Hostname:            "horizon-burst-aaaa1111",
		HeadscaleNodeID:     "99",
		HeadscalePreAuthKey: "key-x",
		HetznerServerID:     "10",
	}
	seededState(t, stateDir, st)

	hs := &mockHeadscaler{}
	prov := &mockHetznerProvider{burstID: "ccdd3344", hostname: "horizon-burst-zzzz9999"}
	kc := fake.NewSimpleClientset()

	if err := cli.RunDownForTest(context.Background(), newTestApp(), hs, prov, kc, stateDir, st); err != nil {
		t.Fatalf("RunDownForTest: %v", err)
	}

	if len(hs.deleteCalls) != 1 || hs.deleteCalls[0] != "99" {
		t.Errorf("DeleteNode called with %v, want [99] (persisted node id)", hs.deleteCalls)
	}
}

func TestDownContinuesOnHeadscaleNodeNotFound(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:             "eeff5566",
		Hostname:            "horizon-burst-eeff5566",
		HeadscaleNodeID:     "",
		HeadscalePreAuthKey: "key-y",
		HetznerServerID:     "20",
	}
	seededState(t, stateDir, st)

	hs := &mockHeadscaler{findNodeID: ""}
	prov := &mockHetznerProvider{burstID: "eeff5566", hostname: "horizon-burst-eeff5566"}
	kc := fake.NewSimpleClientset()

	if err := cli.RunDownForTest(context.Background(), newTestApp(), hs, prov, kc, stateDir, st); err != nil {
		t.Fatalf("expected success when headscale node not found, got: %v", err)
	}

	if len(hs.deleteCalls) != 0 {
		t.Errorf("DeleteNode called unexpectedly: %v", hs.deleteCalls)
	}
}

func TestDownNoBurstIDFlagSelectsSingleState(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	st := cli.BurstState{
		BurstID:             "ffff7777",
		Hostname:            "horizon-burst-ffff7777",
		HeadscaleNodeID:     "5",
		HeadscalePreAuthKey: "key-z",
		HetznerServerID:     "30",
	}
	seededState(t, stateDir, st)

	hs := &mockHeadscaler{}
	prov := &mockHetznerProvider{burstID: "ffff7777", hostname: "horizon-burst-ffff7777"}
	kc := fake.NewSimpleClientset()

	resolved, err := cli.ResolveBurstIDForTest(stateDir, "")
	if err != nil {
		t.Fatalf("ResolveBurstID: %v", err)
	}
	if resolved != "ffff7777" {
		t.Errorf("resolved = %q, want ffff7777", resolved)
	}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), hs, prov, kc, stateDir, st); err != nil {
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
		t.Fatal("expected error for multiple state files, got nil")
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
		t.Errorf("zero-state error = %v, want error containing 'burst-id'", zeroErr)
	}
}

func TestDownEvictsNonDaemonSetPods(t *testing.T) {
	stateDir := t.TempDir()
	restore := cli.SetStateDirForTest(stateDir)
	defer restore()

	hostname := "horizon-burst-aabb9999"
	st := cli.BurstState{
		BurstID:             "aabb9999",
		Hostname:            hostname,
		HeadscaleNodeID:     "3",
		HeadscalePreAuthKey: "key-evict",
		HetznerServerID:     "50",
	}
	seededState(t, stateDir, st)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: hostname},
	}
	deployPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deploy-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "rs-1"},
			},
		},
		Spec: corev1.PodSpec{NodeName: hostname},
	}
	dsPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "DaemonSet", Name: "ds-1"},
			},
		},
		Spec: corev1.PodSpec{NodeName: hostname},
	}

	kc := fake.NewSimpleClientset(node, deployPod, dsPod)

	hs := &mockHeadscaler{}
	prov := &mockHetznerProvider{burstID: "aabb9999", hostname: hostname}

	if err := cli.RunDownForTest(context.Background(), newTestApp(), hs, prov, kc, stateDir, st); err != nil {
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
	for _, a := range evictActions {
		ca, ok := a.(k8stesting.CreateAction)
		if !ok {
			continue
		}
		obj := ca.GetObject()
		t.Logf("evicted object: %T %v", obj, obj)
	}

	if _, statErr := os.Stat(stateDir + "/" + "aabb9999.json"); !os.IsNotExist(statErr) {
		t.Error("state file should be deleted after successful down")
	}
}
