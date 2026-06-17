package core

import (
	"context"

	"github.com/lucawalz/horizon/internal/capi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type WorkloadSummary struct {
	Err          error
	Running      int
	Pending      int
	Failed       int
	Succeeded    int
	CrashLoop    int
	Deployments  WorkloadKind
	StatefulSets WorkloadKind
	DaemonSets   WorkloadKind
}

type WorkloadKind struct {
	Ready    int
	Desired  int
	Degraded int
}

type NodePressure struct {
	Name   string
	Disk   bool
	Memory bool
	PID    bool
}

type NodeHealthSummary struct {
	Err         error
	Pressured   []NodePressure
	CPURequests int64
	CPUAlloc    int64
	MemRequests int64
	MemAlloc    int64
}

func (s NodeHealthSummary) CPUPercent() int {
	return committedPercent(s.CPURequests, s.CPUAlloc)
}

func (s NodeHealthSummary) MemPercent() int {
	return committedPercent(s.MemRequests, s.MemAlloc)
}

func committedPercent(requests, allocatable int64) int {
	if allocatable <= 0 {
		return 0
	}
	return int(requests * 100 / allocatable)
}

type FluxSummary struct {
	Err               error
	KustomizationsErr error
	HelmReleasesErr   error
	Kustomizations    FluxKind
	HelmReleases      FluxKind
}

type FluxKind struct {
	Ready    int
	Total    int
	NotReady []string
}

func workloadFromLists(pods *corev1.PodList, deps, sts, ds WorkloadKind) WorkloadSummary {
	w := WorkloadSummary{Deployments: deps, StatefulSets: sts, DaemonSets: ds}
	if pods == nil {
		return w
	}
	for i := range pods.Items {
		switch pods.Items[i].Status.Phase {
		case corev1.PodRunning:
			w.Running++
		case corev1.PodPending:
			w.Pending++
		case corev1.PodFailed:
			w.Failed++
		case corev1.PodSucceeded:
			w.Succeeded++
		}
		if podCrashLooping(&pods.Items[i]) {
			w.CrashLoop++
		}
	}
	return w
}

func podCrashLooping(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

func nodeHealthFromLists(nodes []corev1.Node, pods *corev1.PodList) NodeHealthSummary {
	s := NodeHealthSummary{}
	for i := range nodes {
		p := nodePressure(&nodes[i])
		if p.Disk || p.Memory || p.PID {
			s.Pressured = append(s.Pressured, p)
		}
		s.CPUAlloc += nodes[i].Status.Allocatable.Cpu().MilliValue()
		s.MemAlloc += nodes[i].Status.Allocatable.Memory().Value()
	}
	if pods != nil {
		for i := range pods.Items {
			for _, c := range pods.Items[i].Spec.Containers {
				s.CPURequests += c.Resources.Requests.Cpu().MilliValue()
				s.MemRequests += c.Resources.Requests.Memory().Value()
			}
		}
	}
	return s
}

func nodePressure(node *corev1.Node) NodePressure {
	p := NodePressure{Name: node.Name}
	for _, cond := range node.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case corev1.NodeDiskPressure:
			p.Disk = true
		case corev1.NodeMemoryPressure:
			p.Memory = true
		case corev1.NodePIDPressure:
			p.PID = true
		}
	}
	return p
}

func fluxKind(resources []capi.FluxResource) FluxKind {
	k := FluxKind{Total: len(resources)}
	for _, r := range resources {
		if r.Ready {
			k.Ready++
			continue
		}
		k.NotReady = append(k.NotReady, r.Name)
	}
	return k
}

func fluxSummary(ctx context.Context, app *App) FluxSummary {
	var s FluxSummary
	kustomizations, kErr := app.CapiClient.ListKustomizations(ctx)
	if kErr != nil {
		s.KustomizationsErr = kErr
	} else {
		s.Kustomizations = fluxKind(kustomizations)
	}
	helmReleases, hErr := app.CapiClient.ListHelmReleases(ctx)
	if hErr != nil {
		s.HelmReleasesErr = hErr
	} else {
		s.HelmReleases = fluxKind(helmReleases)
	}
	if kErr != nil && hErr != nil {
		s.Err = kErr
	}
	return s
}

func workloadKindsFromAPI(ctx context.Context, app *App) (deps, sts, ds WorkloadKind, err error) {
	deployments, err := app.KubeClient.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return deps, sts, ds, err
	}
	for i := range deployments.Items {
		desired := int(replicasOrOne(deployments.Items[i].Spec.Replicas))
		ready := int(deployments.Items[i].Status.ReadyReplicas)
		deps.Desired += desired
		deps.Ready += ready
		if ready < desired {
			deps.Degraded++
		}
	}
	statefulSets, err := app.KubeClient.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return deps, sts, ds, err
	}
	for i := range statefulSets.Items {
		desired := int(replicasOrOne(statefulSets.Items[i].Spec.Replicas))
		ready := int(statefulSets.Items[i].Status.ReadyReplicas)
		sts.Desired += desired
		sts.Ready += ready
		if ready < desired {
			sts.Degraded++
		}
	}
	daemonSets, err := app.KubeClient.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return deps, sts, ds, err
	}
	for i := range daemonSets.Items {
		desired := int(daemonSets.Items[i].Status.DesiredNumberScheduled)
		ready := int(daemonSets.Items[i].Status.NumberReady)
		ds.Desired += desired
		ds.Ready += ready
		if ready < desired {
			ds.Degraded++
		}
	}
	return deps, sts, ds, nil
}

func replicasOrOne(n *int32) int32 {
	if n == nil {
		return 1
	}
	return *n
}
