package cli_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/cli"
	"github.com/lucawalz/horizon/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestApp() *cli.App {
	return &cli.App{
		Config: &config.Config{
			Cluster: "burst",
			Pools:   config.PoolDefaults{Namespace: "caph-system", Cluster: "burst", Name: "burst-workers"},
		},
	}
}

func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func capiScheme(t *testing.T) *crfake.ClientBuilder {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	return crfake.NewClientBuilder().WithScheme(s)
}

func machineDeployment(namespace, name, cluster string, replicas int32) *clusterv1.MachineDeployment {
	return &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: cluster,
			Replicas:    &replicas,
		},
	}
}

func initializedCluster(namespace, name string, initialized bool) *clusterv1.Cluster {
	c := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
	c.Status.Initialization.ControlPlaneInitialized = &initialized
	return c
}

func capiClient(t *testing.T, objs ...client.Object) *capi.Client {
	t.Helper()
	cl := capiScheme(t).WithObjects(objs...).WithStatusSubresource(&clusterv1.Cluster{}).Build()
	return capi.NewClientWithCRClient(cl)
}
