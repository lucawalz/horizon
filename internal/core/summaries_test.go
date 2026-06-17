package core_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/core"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fluxResource(apiVersion, kind, name string, ready bool) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "flux-system"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": status}},
		},
	}}
}

func fluxCapiClient(t *testing.T, objs ...client.Object) *capi.Client {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	kinds := map[schema.GroupVersion]string{
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1"}: "Kustomization",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2"}:      "HelmRelease",
	}
	for gv, kind := range kinds {
		s.AddKnownTypeWithName(gv.WithKind(kind), &unstructured.Unstructured{})
		s.AddKnownTypeWithName(gv.WithKind(kind+"List"), &unstructured.UnstructuredList{})
	}
	cl := crfake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return capi.NewClientWithCRClient(cl)
}

func podWithPhase(name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func crashLoopPod(name string) *corev1.Pod {
	pod := podWithPhase(name, corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
	}
	return pod
}

func podWithRequests(name, cpu, mem string) *corev1.Pod {
	pod := podWithPhase(name, corev1.PodRunning)
	pod.Spec.NodeName = "worker-1"
	pod.Spec.Containers = []corev1.Container{{
		Name: "app",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
		},
	}}
	return pod
}

func deployment(name string, desired, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: &desired},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func TestBuildSnapshotWorkloadCounts(t *testing.T) {
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(
		podWithPhase("a", corev1.PodRunning),
		podWithPhase("b", corev1.PodPending),
		podWithPhase("c", corev1.PodFailed),
		crashLoopPod("d"),
		deployment("web", 3, 2),
		deployment("api", 2, 2),
	)
	app.CapiClient = burstCapiClient(t)

	snap := core.BuildSnapshot(context.Background(), app)
	w := snap.Workload
	if w.Err != nil {
		t.Fatalf("workload err: %v", w.Err)
	}
	if w.Running != 2 || w.Pending != 1 || w.Failed != 1 {
		t.Errorf("phase counts = %+v, want running 2 pending 1 failed 1", w)
	}
	if w.CrashLoop != 1 {
		t.Errorf("crashloop = %d, want 1", w.CrashLoop)
	}
	if w.Deployments.Ready != 4 || w.Deployments.Desired != 5 || w.Deployments.Degraded != 1 {
		t.Errorf("deployments = %+v, want ready 4 desired 5 degraded 1", w.Deployments)
	}
}

func TestBuildSnapshotNodeHealthHeadroom(t *testing.T) {
	node := nodeWithAllocatable("worker-1", "4", "8Gi")
	node.Status.Conditions = append(node.Status.Conditions,
		corev1.NodeCondition{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue})

	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset(
		node,
		podWithRequests("p1", "1", "2Gi"),
		podWithRequests("p2", "1", "2Gi"),
	)
	app.CapiClient = burstCapiClient(t)

	snap := core.BuildSnapshot(context.Background(), app)
	h := snap.NodeHealth
	if h.Err != nil {
		t.Fatalf("node health err: %v", h.Err)
	}
	if len(h.Pressured) != 1 || !h.Pressured[0].Disk {
		t.Errorf("pressured = %+v, want one disk-pressured node", h.Pressured)
	}
	if h.CPUPercent() != 50 {
		t.Errorf("cpu committed = %d%%, want 50", h.CPUPercent())
	}
	if h.MemPercent() != 50 {
		t.Errorf("mem committed = %d%%, want 50", h.MemPercent())
	}
}

func TestFluxKindReadyCounts(t *testing.T) {
	app := newTestApp()
	app.KubeClient = fake.NewSimpleClientset()
	app.CapiClient = fluxCapiClient(t,
		fluxResource("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "infra", true),
		fluxResource("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "apps", false),
		fluxResource("helm.toolkit.fluxcd.io/v2", "HelmRelease", "traefik", true),
	)

	snap := core.BuildSnapshot(context.Background(), app)
	f := snap.Flux
	if f.KustomizationsErr != nil || f.HelmReleasesErr != nil {
		t.Fatalf("flux errs: %v %v", f.KustomizationsErr, f.HelmReleasesErr)
	}
	if f.Kustomizations.Ready != 1 || f.Kustomizations.Total != 2 {
		t.Errorf("kustomizations = %+v, want ready 1 total 2", f.Kustomizations)
	}
	if len(f.Kustomizations.NotReady) != 1 || f.Kustomizations.NotReady[0] != "apps" {
		t.Errorf("not-ready = %+v, want [apps]", f.Kustomizations.NotReady)
	}
	if f.HelmReleases.Ready != 1 || f.HelmReleases.Total != 1 {
		t.Errorf("helmreleases = %+v, want ready 1 total 1", f.HelmReleases)
	}
}
