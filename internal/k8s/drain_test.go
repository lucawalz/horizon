package k8s_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func boolPtr(b bool) *bool { return &b }

func evictAndDelete(kc *fake.Clientset) {
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
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		ca := action.(k8stesting.CreateAction)
		if ev, ok := ca.GetObject().(interface{ GetName() string }); ok {
			_ = kc.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), action.GetNamespace(), ev.GetName())
		}
		return true, nil, nil
	})
}

func TestDrain(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	appPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "burst-node"},
	}
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds-1", Namespace: "default", UID: "u"}}
	dsPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "DaemonSet",
				Name:       "ds-1",
				APIVersion: "apps/v1",
				UID:        "u",
				Controller: boolPtr(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "burst-node"},
	}
	kc := fake.NewSimpleClientset(node, appPod, ds, dsPod)
	evictAndDelete(kc)

	if err := k8s.Drain(context.Background(), kc, "burst-node", 30*time.Second, nil); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	var evictActions []k8stesting.Action
	for _, a := range kc.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			evictActions = append(evictActions, a)
		}
	}
	if len(evictActions) != 1 {
		t.Errorf("eviction count = %d, want 1 (only app, not ds-pod)", len(evictActions))
	}
	if len(evictActions) == 1 {
		ca, ok := evictActions[0].(k8stesting.CreateAction)
		if ok {
			obj := ca.GetObject()
			if ev, ok2 := obj.(interface{ GetName() string }); ok2 {
				if ev.GetName() != "app" {
					t.Errorf("evicted pod = %q, want app", ev.GetName())
				}
			}
		}
	}

	n, err := kc.CoreV1().Nodes().Get(context.Background(), "burst-node", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !n.Spec.Unschedulable {
		t.Error("node should be cordoned (Unschedulable=true) after drain")
	}
}

func TestDrain_DaemonSetSkip(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	appPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "burst-node"},
	}
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds-1", Namespace: "default", UID: "u"}}
	dsPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "DaemonSet",
				Name:       "ds-1",
				APIVersion: "apps/v1",
				UID:        "u",
				Controller: boolPtr(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "burst-node"},
	}
	kc := fake.NewSimpleClientset(node, appPod, ds, dsPod)
	evictAndDelete(kc)

	if err := k8s.Drain(context.Background(), kc, "burst-node", 30*time.Second, nil); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	for _, a := range kc.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			if ca, ok := a.(k8stesting.CreateAction); ok {
				if ev, ok2 := ca.GetObject().(interface{ GetName() string }); ok2 {
					if ev.GetName() == "ds-pod" {
						t.Error("DaemonSet pod ds-pod should not be evicted")
					}
				}
			}
		}
	}
}

func TestDrain_Timeout(t *testing.T) {
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

	err := k8s.Drain(context.Background(), kc, "burst-node", 100*time.Millisecond, nil)
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

