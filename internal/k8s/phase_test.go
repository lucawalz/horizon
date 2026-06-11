package k8s_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func enforceConfigMapConflicts(kc *fake.Clientset) {
	var mu sync.Mutex
	gr := schema.GroupResource{Resource: "configmaps"}
	store := map[string]*corev1.ConfigMap{}
	rv := 0
	key := func(ns, name string) string { return ns + "/" + name }

	kc.PrependReactor("get", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		ga := action.(k8stesting.GetAction)
		cm, ok := store[key(ga.GetNamespace(), ga.GetName())]
		if !ok {
			return true, nil, apierrors.NewNotFound(gr, ga.GetName())
		}
		return true, cm.DeepCopy(), nil
	})
	kc.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		cm := action.(k8stesting.CreateAction).GetObject().(*corev1.ConfigMap).DeepCopy()
		k := key(cm.Namespace, cm.Name)
		if _, ok := store[k]; ok {
			return true, nil, apierrors.NewAlreadyExists(gr, cm.Name)
		}
		rv++
		cm.ResourceVersion = strconv.Itoa(rv)
		store[k] = cm
		return true, cm.DeepCopy(), nil
	})
	kc.PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		cm := action.(k8stesting.UpdateAction).GetObject().(*corev1.ConfigMap).DeepCopy()
		cur, ok := store[key(cm.Namespace, cm.Name)]
		if !ok {
			return true, nil, apierrors.NewNotFound(gr, cm.Name)
		}
		if cur.ResourceVersion != cm.ResourceVersion {
			return true, nil, apierrors.NewConflict(gr, cm.Name, nil)
		}
		rv++
		cm.ResourceVersion = strconv.Itoa(rv)
		store[key(cm.Namespace, cm.Name)] = cm
		return true, cm.DeepCopy(), nil
	})
}

func TestBurstPhases_PerIDRoundTrip(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	if err := k8s.WriteBurstPhase(ctx, kc, "aaaa1111", k8s.BurstPhaseBackingUp); err != nil {
		t.Fatalf("WriteBurstPhase aaaa1111: %v", err)
	}
	if err := k8s.WriteBurstPhase(ctx, kc, "bbbb2222", k8s.BurstPhaseProvisioning); err != nil {
		t.Fatalf("WriteBurstPhase bbbb2222: %v", err)
	}

	phases, err := k8s.ReadBurstPhases(ctx, kc)
	if err != nil {
		t.Fatalf("ReadBurstPhases: %v", err)
	}
	if phases["aaaa1111"] != k8s.BurstPhaseBackingUp {
		t.Errorf("aaaa1111 = %q, want %q", phases["aaaa1111"], k8s.BurstPhaseBackingUp)
	}
	if phases["bbbb2222"] != k8s.BurstPhaseProvisioning {
		t.Errorf("bbbb2222 = %q, want %q", phases["bbbb2222"], k8s.BurstPhaseProvisioning)
	}

	if err := k8s.WriteBurstPhase(ctx, kc, "aaaa1111", k8s.BurstPhaseRunning); err != nil {
		t.Fatalf("WriteBurstPhase update aaaa1111: %v", err)
	}
	phases, _ = k8s.ReadBurstPhases(ctx, kc)
	if phases["aaaa1111"] != k8s.BurstPhaseRunning {
		t.Errorf("aaaa1111 after update = %q, want %q", phases["aaaa1111"], k8s.BurstPhaseRunning)
	}
	if phases["bbbb2222"] != k8s.BurstPhaseProvisioning {
		t.Errorf("bbbb2222 must be untouched, got %q", phases["bbbb2222"])
	}
}

func TestBurstPhases_Prune(t *testing.T) {
	kc := fake.NewSimpleClientset()
	ctx := context.Background()

	_ = k8s.WriteBurstPhase(ctx, kc, "aaaa1111", k8s.BurstPhaseRunning)
	_ = k8s.WriteBurstPhase(ctx, kc, "bbbb2222", k8s.BurstPhaseRunning)

	if err := k8s.ClearBurstPhase(ctx, kc, "aaaa1111"); err != nil {
		t.Fatalf("ClearBurstPhase: %v", err)
	}
	phases, _ := k8s.ReadBurstPhases(ctx, kc)
	if _, ok := phases["aaaa1111"]; ok {
		t.Errorf("aaaa1111 should be pruned, got %v", phases)
	}
	if phases["bbbb2222"] != k8s.BurstPhaseRunning {
		t.Errorf("bbbb2222 must survive prune, got %q", phases["bbbb2222"])
	}
}

func TestBurstPhases_EmptyWhenAbsent(t *testing.T) {
	phases, err := k8s.ReadBurstPhases(context.Background(), fake.NewSimpleClientset())
	if err != nil {
		t.Fatalf("ReadBurstPhases: %v", err)
	}
	if len(phases) != 0 {
		t.Errorf("expected empty map, got %v", phases)
	}
}

func TestBurstPhases_ConcurrentWritesNoLostUpdates(t *testing.T) {
	kc := fake.NewSimpleClientset()
	enforceConfigMapConflicts(kc)
	ctx := context.Background()

	ids := []string{"aaaa1111", "bbbb2222", "cccc3333", "dddd4444", "eeee5555"}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(burstID string) {
			defer wg.Done()
			if err := k8s.WriteBurstPhase(ctx, kc, burstID, k8s.BurstPhaseRunning); err != nil {
				t.Errorf("WriteBurstPhase %s: %v", burstID, err)
			}
		}(id)
	}
	wg.Wait()

	phases, err := k8s.ReadBurstPhases(ctx, kc)
	if err != nil {
		t.Fatalf("ReadBurstPhases: %v", err)
	}
	for _, id := range ids {
		if phases[id] != k8s.BurstPhaseRunning {
			t.Errorf("lost update for %s: got %q, want %q", id, phases[id], k8s.BurstPhaseRunning)
		}
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
