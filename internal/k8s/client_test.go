package k8s_test

import (
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
)

func TestInCluster(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
		if !k8s.InCluster() {
			t.Fatal("expected InCluster() true when KUBERNETES_SERVICE_HOST is set")
		}
	})
	t.Run("unset", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		if k8s.InCluster() {
			t.Fatal("expected InCluster() false when KUBERNETES_SERVICE_HOST is empty")
		}
	})
}
