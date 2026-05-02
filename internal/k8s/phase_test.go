package k8s_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBurstPhase_RoundTrip(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	if err := k8s.WriteBurstPhase(ctx, kc, k8s.BurstPhaseBackingUp); err != nil {
		t.Fatalf("WriteBurstPhase BackingUp: %v", err)
	}
	if got := k8s.ReadBurstPhase(ctx, kc); got != k8s.BurstPhaseBackingUp {
		t.Errorf("ReadBurstPhase = %q, want %q", got, k8s.BurstPhaseBackingUp)
	}

	if err := k8s.WriteBurstPhase(ctx, kc, k8s.BurstPhaseProvisioning); err != nil {
		t.Fatalf("WriteBurstPhase Provisioning: %v", err)
	}
	if got := k8s.ReadBurstPhase(ctx, kc); got != k8s.BurstPhaseProvisioning {
		t.Errorf("ReadBurstPhase = %q, want %q", got, k8s.BurstPhaseProvisioning)
	}
}

func TestBurstPhase_FallbackIdle(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if got := k8s.ReadBurstPhase(context.Background(), kc); got != k8s.BurstPhaseIdle {
		t.Errorf("ReadBurstPhase = %q, want %q", got, k8s.BurstPhaseIdle)
	}
}

func TestBurstPhase_NilData(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "horizon-state", Namespace: "kube-system"}}
	kc := fake.NewSimpleClientset(cm)
	if got := k8s.ReadBurstPhase(context.Background(), kc); got != k8s.BurstPhaseIdle {
		t.Errorf("ReadBurstPhase = %q, want %q", got, k8s.BurstPhaseIdle)
	}
}

func TestBurstPhase_EmptyValue(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-state", Namespace: "kube-system"},
		Data:       map[string]string{"burst_phase": ""},
	}
	kc := fake.NewSimpleClientset(cm)
	if got := k8s.ReadBurstPhase(context.Background(), kc); got != k8s.BurstPhaseIdle {
		t.Errorf("ReadBurstPhase = %q, want %q", got, k8s.BurstPhaseIdle)
	}
}

func TestBurstPhase_AllConstants(t *testing.T) {
	cases := []struct {
		name string
		want string
		got  string
	}{
		{"Idle", "Idle", k8s.BurstPhaseIdle},
		{"BackingUp", "BackingUp", k8s.BurstPhaseBackingUp},
		{"Provisioning", "Provisioning", k8s.BurstPhaseProvisioning},
		{"Joining", "Joining", k8s.BurstPhaseJoining},
		{"Migrating", "Migrating", k8s.BurstPhaseMigrating},
		{"Running", "Running", k8s.BurstPhaseRunning},
		{"TearingDown", "TearingDown", k8s.BurstPhaseTearingDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("BurstPhase%s = %q, want %q", c.name, c.got, c.want)
			}
		})
	}
}
