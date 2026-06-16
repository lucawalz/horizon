package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/core"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const drainTestTimeout = 100 * time.Millisecond

func TestDrainEmptyNode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	kc := fake.NewSimpleClientset(node)

	if err := core.Drain(context.Background(), kc, "burst-node", drainTestTimeout, nil); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

func TestDrainTimesOutWhenEvictionBlocked(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	appPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "burst-node"},
	}
	kc := fake.NewSimpleClientset(node, appPod)
	kc.Resources = append(kc.Resources, &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{
			Name:    "pods/eviction",
			Kind:    "Eviction",
			Group:   "policy",
			Version: "v1",
		}},
	})
	kc.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "eviction" {
			return true, nil, apierrors.NewTooManyRequests("pdb blocks eviction", 1)
		}
		return false, nil, nil
	})

	err := core.Drain(context.Background(), kc, "burst-node", drainTestTimeout, nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "app") {
		t.Errorf("error %q does not contain pod name 'app'", err.Error())
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q does not contain 'timeout'", err.Error())
	}
}
