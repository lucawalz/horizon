package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDrainCommand(t *testing.T) {
	t.Skip("Plan 04 implements internal/cli/drain.go")

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	kc := fake.NewSimpleClientset(node)

	var err error
	out := captureStdout(func() {
		err = cli.RunDrainForTest(context.Background(), kc, "burst-node")
	})
	if err != nil {
		t.Fatalf("RunDrainForTest: %v", err)
	}
	if !strings.Contains(out, "0 non-DaemonSet pods remain on burst-node") {
		t.Errorf("output %q does not contain expected drain summary", out)
	}
}

func TestDrainCommand_Timeout(t *testing.T) {
	t.Skip("Plan 04 implements internal/cli/drain.go")

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node"}}
	appPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "burst-node"},
	}
	kc := fake.NewSimpleClientset(node, appPod)
	kc.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "eviction" {
			return true, nil, apierrors.NewTooManyRequests("pdb blocks eviction", 1)
		}
		return false, nil, nil
	})

	err := cli.RunDrainForTest(context.Background(), kc, "burst-node")
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
