package hetzner_test

import (
	"context"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider/hetzner"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWaitNodeReady_ReturnsWhenNamedNodeReady(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-burst-aabb1234"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	kc := fake.NewSimpleClientset(node)

	if err := hetzner.WaitNodeReady(context.Background(), kc, "horizon-burst-aabb1234", 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatalf("WaitNodeReady: %v", err)
	}
}

func TestWaitNodeReady_TimesOut(t *testing.T) {
	other := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-burst-other"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	kc := fake.NewSimpleClientset(other)

	err := hetzner.WaitNodeReady(context.Background(), kc, "horizon-burst-missing", 100*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error when the named node never appears")
	}
}
