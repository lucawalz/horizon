package hetzner_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

func nodeReady(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func nodeNotReady(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

func flannelPod(nodeName, podName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "flannel"},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

func TestWaitNodeReadySuccess(t *testing.T) {
	name := "horizon-burst-abc1"
	kc := fake.NewSimpleClientset(nodeReady(name), flannelPod(name, "flannel-x"))
	err := hetzner.WaitNodeReady(context.Background(), kc, name, 100*time.Millisecond, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitNodeReadyTimeout(t *testing.T) {
	name := "horizon-burst-abc2"
	kc := fake.NewSimpleClientset(nodeNotReady(name))
	err := hetzner.WaitNodeReady(context.Background(), kc, name, 100*time.Millisecond, 25*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	errStr := err.Error()
	if !containsAll(errStr, "timeout", name) {
		t.Errorf("error %q must contain 'timeout' and %q", errStr, name)
	}
}

func TestWaitNodeReadyNoFlannelPod(t *testing.T) {
	name := "horizon-burst-abc3"
	kc := fake.NewSimpleClientset(nodeReady(name))
	err := hetzner.WaitNodeReady(context.Background(), kc, name, 100*time.Millisecond, 25*time.Millisecond)
	if err == nil {
		t.Fatal("expected flannel error, got nil")
	}
	if !containsAll(err.Error(), "flannel") {
		t.Errorf("error %q must contain 'flannel'", err.Error())
	}
}

func TestWaitNodeReadyContextCancelled(t *testing.T) {
	name := "horizon-burst-abc4"
	kc := fake.NewSimpleClientset(nodeNotReady(name))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := hetzner.WaitNodeReady(ctx, kc, name, 5*time.Second, 25*time.Millisecond)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}
