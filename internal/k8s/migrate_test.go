package k8s_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func makePod(name, ns, node string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func makeDSPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "DaemonSet",
				Name:       "ds",
				APIVersion: "apps/v1",
				UID:        "u",
				Controller: boolPtr(true),
			}},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestMigrateEviction(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1"}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "sentio-systems"}}
	appPod := makePod("app-pod", "sentio-systems", "homelab-1", corev1.PodRunning)
	dsPod := makeDSPod("ds-pod", "sentio-systems", "homelab-1")
	otherPod := makePod("other-pod", "default", "homelab-1", corev1.PodRunning)

	kc := fake.NewSimpleClientset(node, dep, appPod, dsPod, otherPod)
	evictAndDelete(kc)

	state, err := k8s.Migrate(context.Background(), kc, "sentio-systems", "burst-1")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if state == nil {
		t.Fatal("Migrate returned nil state")
	}

	var evictions []k8stesting.CreateAction
	for _, a := range kc.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			evictions = append(evictions, a.(k8stesting.CreateAction))
		}
	}
	if len(evictions) != 1 {
		t.Fatalf("eviction count = %d, want 1", len(evictions))
	}
	ev := evictions[0].GetObject().(interface{ GetName() string })
	if ev.GetName() != "app-pod" {
		t.Errorf("evicted pod = %q, want app-pod", ev.GetName())
	}
}

func TestMigrateNodeLabel(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1"}}
	kc := fake.NewSimpleClientset(node)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, "sentio-systems", "burst-1"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	n, err := kc.CoreV1().Nodes().Get(context.Background(), "burst-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if got := n.Labels[k8s.NodeAffinityLabelKey]; got != "sentio-systems" {
		t.Errorf("node label %q = %q, want sentio-systems", k8s.NodeAffinityLabelKey, got)
	}
}

func TestMigrateAffinityPatch(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1"}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"}}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: "sentio-systems"}}
	kc := fake.NewSimpleClientset(node, dep, sts)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, "sentio-systems", "burst-1"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var depPatches, stsPatches []k8stesting.PatchAction
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" {
			continue
		}
		pa := a.(k8stesting.PatchAction)
		switch a.GetResource().Resource {
		case "deployments":
			depPatches = append(depPatches, pa)
		case "statefulsets":
			stsPatches = append(stsPatches, pa)
		}
	}

	if len(depPatches) != 1 {
		t.Errorf("deployment patch count = %d, want 1", len(depPatches))
	} else {
		pa := depPatches[0]
		if pa.GetPatchType() != types.StrategicMergePatchType {
			t.Errorf("deployment patch type = %v, want StrategicMergePatchType", pa.GetPatchType())
		}
		body := string(pa.GetPatch())
		if !strings.Contains(body, "horizon.dev/burst-workload") {
			t.Errorf("deployment patch missing label key: %s", body)
		}
		if !strings.Contains(body, "sentio-systems") {
			t.Errorf("deployment patch missing namespace value: %s", body)
		}
	}

	if len(stsPatches) != 1 {
		t.Errorf("statefulset patch count = %d, want 1", len(stsPatches))
	} else {
		pa := stsPatches[0]
		if pa.GetPatchType() != types.StrategicMergePatchType {
			t.Errorf("statefulset patch type = %v, want StrategicMergePatchType", pa.GetPatchType())
		}
		body := string(pa.GetPatch())
		if !strings.Contains(body, "horizon.dev/burst-workload") {
			t.Errorf("statefulset patch missing label key: %s", body)
		}
		if !strings.Contains(body, "sentio-systems") {
			t.Errorf("statefulset patch missing namespace value: %s", body)
		}
	}
}

func TestMigrateSavesOriginalAffinity(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1"}}

	originalAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: "kubernetes.io/hostname",
				},
			}},
		},
	}

	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: originalAffinity},
			},
		},
	}
	dep2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep2", Namespace: "sentio-systems"},
	}

	kc := fake.NewSimpleClientset(node, dep1, dep2)
	evictAndDelete(kc)

	state, err := k8s.Migrate(context.Background(), kc, "sentio-systems", "burst-1")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := k8s.RollbackMigrate(context.Background(), kc, state); err != nil {
		t.Fatalf("RollbackMigrate: %v", err)
	}

	origJSON, _ := json.Marshal(originalAffinity)
	var dep1RestoreFound, dep2NullFound bool
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" || a.GetResource().Resource != "deployments" {
			continue
		}
		pa := a.(k8stesting.PatchAction)
		body := string(pa.GetPatch())
		switch pa.GetName() {
		case "dep1":
			if strings.Contains(body, "kubernetes.io/hostname") && !strings.Contains(body, "burst-workload") {
				var got map[string]interface{}
				_ = json.Unmarshal(pa.GetPatch(), &got)
				dep1RestoreFound = strings.Contains(body, string(origJSON[1:len(origJSON)-1]))
				dep1RestoreFound = strings.Contains(body, "podAntiAffinity") && !strings.Contains(body, "nodeAffinity")
			}
		case "dep2":
			if strings.Contains(body, `"affinity":null`) {
				dep2NullFound = true
			}
		}
	}
	if !dep1RestoreFound {
		t.Error("dep1 rollback patch missing original podAntiAffinity or still contains nodeAffinity")
	}
	if !dep2NullFound {
		t.Error("dep2 rollback patch does not set affinity to null")
	}
}

func TestRollbackMigrate_RestoresAffinityAndRemovesLabel(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1"}}

	existingAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 50,
				PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
			}},
		},
	}

	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Affinity: existingAffinity},
			},
		},
	}
	sts1 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: "sentio-systems"}}

	kc := fake.NewSimpleClientset(node, dep1, sts1)
	evictAndDelete(kc)

	state, err := k8s.Migrate(context.Background(), kc, "sentio-systems", "burst-1")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := k8s.RollbackMigrate(context.Background(), kc, state); err != nil {
		t.Fatalf("RollbackMigrate: %v", err)
	}

	var depPatches, nodePatches []k8stesting.PatchAction
	for _, a := range kc.Actions() {
		if a.GetVerb() != "patch" {
			continue
		}
		pa := a.(k8stesting.PatchAction)
		switch a.GetResource().Resource {
		case "deployments":
			depPatches = append(depPatches, pa)
		case "nodes":
			nodePatches = append(nodePatches, pa)
		}
	}

	if len(depPatches) < 2 {
		t.Errorf("deployment patch count = %d, want >= 2", len(depPatches))
	}
	if len(nodePatches) < 2 {
		t.Errorf("node patch count = %d, want >= 2", len(nodePatches))
	}

	foundRemoval := false
	for _, pa := range nodePatches {
		if pa.GetPatchType() == types.MergePatchType && strings.Contains(string(pa.GetPatch()), `"horizon.dev/burst-workload":null`) {
			foundRemoval = true
		}
	}
	if !foundRemoval {
		t.Error("no node label removal patch found with null value")
	}

	n, err := kc.CoreV1().Nodes().Get(context.Background(), "burst-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if _, exists := n.Labels[k8s.NodeAffinityLabelKey]; exists {
		t.Error("node should not have burst-workload label after rollback")
	}
}

func TestRollbackMigrate_NilState(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if err := k8s.RollbackMigrate(context.Background(), kc, nil); err != nil {
		t.Errorf("RollbackMigrate(nil) = %v, want nil", err)
	}
}

func TestValidateNamespace(t *testing.T) {
	cases := []struct {
		ns      string
		wantErr bool
	}{
		{"sentio-systems", false},
		{"abc", false},
		{"a", false},
		{"ns1-2", false},
		{"", true},
		{"Foo", true},
		{"foo_bar", true},
		{"foo.bar", true},
		{"../../etc", true},
		{strings.Repeat("a", 64), true},
		{"-foo", true},
		{"foo-", true},
	}
	for _, c := range cases {
		err := k8s.ValidateNamespace(c.ns)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateNamespace(%q) error = %v, wantErr = %v", c.ns, err, c.wantErr)
		}
	}
}

func burstNode(name, ns string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{k8s.NodeAffinityLabelKey: ns},
		},
	}
}

func TestWaitWorkloadOnBurstNodes_SpreadAcrossNodes(t *testing.T) {
	ns := "sentio-systems"
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", ns),
		burstNode("burst-2", ns),
		makePod("p1", ns, "burst-1", corev1.PodRunning),
		makePod("p2", ns, "burst-2", corev1.PodRunning),
	)
	err := k8s.WaitWorkloadOnBurstNodes(context.Background(), kc, ns, 10*time.Millisecond, 1*time.Second)
	if err != nil {
		t.Errorf("WaitWorkloadOnBurstNodes = %v, want nil", err)
	}
}

func TestWaitWorkloadOnBurstNodes_PendingTimesOut(t *testing.T) {
	ns := "sentio-systems"
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", ns),
		makePod("p1", ns, "burst-1", corev1.PodRunning),
		makePod("p2", ns, "burst-1", corev1.PodPending),
	)
	err := k8s.WaitWorkloadOnBurstNodes(context.Background(), kc, ns, 10*time.Millisecond, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "wait-pods") || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q missing wait-pods/timeout", err.Error())
	}
}

func TestWaitWorkloadOnBurstNodes_UnlabeledNodeTimesOut(t *testing.T) {
	ns := "sentio-systems"
	kc := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "homelab-1"}},
		makePod("p1", ns, "homelab-1", corev1.PodRunning),
	)
	err := k8s.WaitWorkloadOnBurstNodes(context.Background(), kc, ns, 10*time.Millisecond, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout when pod sits on an unlabeled node")
	}
}

func TestWaitWorkloadOnBurstNodes_DaemonSetIgnored(t *testing.T) {
	ns := "sentio-systems"
	kc := fake.NewSimpleClientset(
		burstNode("burst-1", ns),
		makePod("app", ns, "burst-1", corev1.PodRunning),
		makeDSPod("ds-pod", ns, "homelab-1"),
	)
	err := k8s.WaitWorkloadOnBurstNodes(context.Background(), kc, ns, 10*time.Millisecond, 1*time.Second)
	if err != nil {
		t.Errorf("WaitWorkloadOnBurstNodes = %v, want nil", err)
	}
}

func TestWaitWorkloadOnBurstNodes_EmptyNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	err := k8s.WaitWorkloadOnBurstNodes(context.Background(), kc, "", 10*time.Millisecond, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for empty namespace, got nil")
	}
}
