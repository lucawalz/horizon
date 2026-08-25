package k8s_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func classifyOne(t *testing.T, object runtime.Object) k8s.WorkloadMigratability {
	t.Helper()
	kc := fake.NewSimpleClientset(object)
	got, err := k8s.ClassifyMigratability(context.Background(), kc, testNS, testLease)
	if err != nil {
		t.Fatalf("ClassifyMigratability: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("assessments = %+v, want exactly one", got)
	}
	return got[0]
}

func deploymentWith(name string, replicas int32, mutate func(spec *appsv1.DeploymentSpec)) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	mutate(&dep.Spec)
	return dep
}

func statefulSetWith(name string, replicas int32, mutate func(spec *appsv1.StatefulSetSpec)) *appsv1.StatefulSet {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	mutate(&sts.Spec)
	return sts
}

func rollingUpdateSurge(surge intstr.IntOrString) func(spec *appsv1.DeploymentSpec) {
	return func(spec *appsv1.DeploymentSpec) {
		spec.Strategy = appsv1.DeploymentStrategy{
			Type:          appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &surge},
		}
	}
}

func TestClassifyMigratabilityNamesEveryDisruptiveShape(t *testing.T) {
	partition := int32(2)
	cases := []struct {
		name    string
		object  runtime.Object
		ref     string
		reasons []string
	}{
		{
			name:    "a rolling deployment on its defaults",
			object:  deploymentWith("dep1", 3, func(*appsv1.DeploymentSpec) {}),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name: "a recreate deployment",
			object: deploymentWith("dep1", 3, func(spec *appsv1.DeploymentSpec) {
				spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
			}),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonRecreateStrategy},
		},
		{
			name:    "a rolling deployment that may not surge",
			object:  deploymentWith("dep1", 3, rollingUpdateSurge(intstr.FromInt32(0))),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonNoSurgeCapacity},
		},
		{
			name:    "a rolling deployment whose surge percentage rounds to nothing",
			object:  deploymentWith("dep1", 3, rollingUpdateSurge(intstr.FromString("0%"))),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonNoSurgeCapacity},
		},
		{
			name:    "a rolling deployment whose surge percentage rounds up to one pod",
			object:  deploymentWith("dep1", 3, rollingUpdateSurge(intstr.FromString("1%"))),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name:    "a deployment scaled to zero",
			object:  deploymentWith("dep1", 0, rollingUpdateSurge(intstr.FromString("25%"))),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name: "a paused deployment",
			object: deploymentWith("dep1", 3, func(spec *appsv1.DeploymentSpec) {
				spec.Paused = true
			}),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonRolloutPaused},
		},
		{
			name: "a workload pinned by node selector",
			object: deploymentWith("dep1", 3, func(spec *appsv1.DeploymentSpec) {
				spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
			}),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonNodeSelectorPinned},
		},
		{
			name: "a deployment carrying more than one impediment",
			object: deploymentWith("dep1", 3, func(spec *appsv1.DeploymentSpec) {
				spec.Paused = true
				spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
				spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
			}),
			ref:     "deployment/dep1",
			reasons: []string{k8s.ReasonRolloutPaused, k8s.ReasonRecreateStrategy, k8s.ReasonNodeSelectorPinned},
		},
		{
			name: "a rolling statefulset",
			object: statefulSetWith("sts1", 3, func(spec *appsv1.StatefulSetSpec) {
				spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType}
			}),
			ref:     "statefulset/sts1",
			reasons: nil,
		},
		{
			name: "an ondelete statefulset",
			object: statefulSetWith("sts1", 3, func(spec *appsv1.StatefulSetSpec) {
				spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
			}),
			ref:     "statefulset/sts1",
			reasons: []string{k8s.ReasonManualRollout},
		},
		{
			name: "a partitioned statefulset",
			object: statefulSetWith("sts1", 3, func(spec *appsv1.StatefulSetSpec) {
				spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
					Type:          appsv1.RollingUpdateStatefulSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition},
				}
			}),
			ref:     "statefulset/sts1",
			reasons: []string{k8s.ReasonPartitionedRollout},
		},
		{
			name: "a recreate deployment scaled to zero",
			object: deploymentWith("dep1", 0, func(spec *appsv1.DeploymentSpec) {
				spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
			}),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name: "a paused deployment scaled to zero",
			object: deploymentWith("dep1", 0, func(spec *appsv1.DeploymentSpec) {
				spec.Paused = true
			}),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name: "a pinned deployment scaled to zero",
			object: deploymentWith("dep1", 0, func(spec *appsv1.DeploymentSpec) {
				spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
			}),
			ref:     "deployment/dep1",
			reasons: nil,
		},
		{
			name: "an ondelete statefulset scaled to zero",
			object: statefulSetWith("sts1", 0, func(spec *appsv1.StatefulSetSpec) {
				spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
			}),
			ref:     "statefulset/sts1",
			reasons: nil,
		},
		{
			name: "a partitioned statefulset scaled to zero",
			object: statefulSetWith("sts1", 0, func(spec *appsv1.StatefulSetSpec) {
				spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
					Type:          appsv1.RollingUpdateStatefulSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition},
				}
			}),
			ref:     "statefulset/sts1",
			reasons: nil,
		},
		{
			name: "a statefulset pinned by node selector",
			object: statefulSetWith("sts1", 3, func(spec *appsv1.StatefulSetSpec) {
				spec.Template.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
			}),
			ref:     "statefulset/sts1",
			reasons: []string{k8s.ReasonNodeSelectorPinned},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOne(t, tc.object)
			if got.Workload != tc.ref {
				t.Errorf("workload = %q, want %q", got.Workload, tc.ref)
			}
			if !reflect.DeepEqual(got.Reasons, tc.reasons) {
				t.Errorf("reasons = %v, want %v", got.Reasons, tc.reasons)
			}
			want := k8s.VerdictSeamless
			if len(tc.reasons) > 0 {
				want = k8s.VerdictDisruptive
			}
			if got.Verdict != want {
				t.Errorf("verdict = %q, want %q", got.Verdict, want)
			}
		})
	}
}

func TestClassifyMigratabilityCoversBothWorkloadKinds(t *testing.T) {
	dep := deploymentWith("dep1", 1, func(*appsv1.DeploymentSpec) {})
	sts := statefulSetWith("sts1", 1, func(spec *appsv1.StatefulSetSpec) {
		spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	})
	elsewhere := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}

	kc := fake.NewSimpleClientset(dep, sts, elsewhere)
	got, err := k8s.ClassifyMigratability(context.Background(), kc, testNS, testLease)
	if err != nil {
		t.Fatalf("ClassifyMigratability: %v", err)
	}

	want := []k8s.WorkloadMigratability{
		{Workload: "deployment/dep1", Verdict: k8s.VerdictSeamless},
		{Workload: "statefulset/sts1", Verdict: k8s.VerdictDisruptive, Reasons: []string{k8s.ReasonManualRollout}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assessments = %+v, want %+v", got, want)
	}
}

func TestClassifyMigratabilityRefusesAnInvalidNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.ClassifyMigratability(context.Background(), kc, "Not Valid", testLease); err == nil {
		t.Fatal("ClassifyMigratability accepted an invalid namespace")
	}
}

func TestClassifyMigratabilityReportsAListFailure(t *testing.T) {
	kc := fake.NewSimpleClientset()
	kc.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api server unreachable")
	})

	_, err := k8s.ClassifyMigratability(context.Background(), kc, testNS, testLease)
	if err == nil || !strings.Contains(err.Error(), "list deployments") {
		t.Fatalf("err = %v, want a deployment list failure", err)
	}
}

func TestMigrateSkipsPodsOfWorkloadsThatRollThemselves(t *testing.T) {
	node := burstNode("burst-1", testLease.UID)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: testNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
		},
	}
	matched := makePod("dep1-pod", testNS, "homelab-1", corev1.PodRunning)
	matched.Labels = map[string]string{"app": "web"}

	kc := fake.NewSimpleClientset(node, dep, matched)
	evictAndDelete(kc)

	if _, err := k8s.Migrate(context.Background(), kc, testNS, testLease); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if evicted := evictionNames(kc); len(evicted) != 0 {
		t.Errorf("evicted = %v, want none: a recreate deployment still rolls its own pods", evicted)
	}
}

func TestClassifyMigratabilityStillFlagsAWorkloadAlreadyMovedOntoBurst(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: `{"nodeSelector":{"disktype":"ssd"}}`},
		},
	}

	got := classifyOne(t, dep)
	want := []string{k8s.ReasonNodeSelectorPinned}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("reasons = %v, want %v: a retried pass reads the pin from the saved placement", got.Reasons, want)
	}
}

func TestClassifyMigratabilityNamesAWorkloadHeldByAnotherLease(t *testing.T) {
	held := migratedMeta("dep1")
	held.Labels[provider.LeaseUIDLabelKey] = "uid-b"
	dep := &appsv1.Deployment{ObjectMeta: held}

	got := classifyOne(t, dep)
	if got.Verdict != k8s.VerdictDisruptive {
		t.Errorf("verdict = %q, want %q", got.Verdict, k8s.VerdictDisruptive)
	}
	want := []string{k8s.ReasonHeldByAnotherLease}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("reasons = %v, want %v: the conflict must reach status before a move is attempted", got.Reasons, want)
	}
}

func TestClassifyMigratabilityAcceptsThisLeasesOwnWorkload(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: migratedMeta("dep1")}

	got := classifyOne(t, dep)
	if got.Verdict != k8s.VerdictSeamless {
		t.Errorf("verdict = %q with reasons %v, want %q", got.Verdict, got.Reasons, k8s.VerdictSeamless)
	}
}

func TestClassifyMigratabilityRefusesAnEmptyIdentity(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if _, err := k8s.ClassifyMigratability(context.Background(), kc, testNS, k8s.LeaseIdentity{}); err == nil {
		t.Fatal("ClassifyMigratability accepted an empty lease identity")
	}
}

func TestClassifyMigratabilityRefusesACorruptPlacementAnnotation(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dep1",
			Namespace:   testNS,
			Annotations: map[string]string{k8s.PrePlacementAnnotationKey: "{not json"},
		},
	}

	kc := fake.NewSimpleClientset(dep)
	_, err := k8s.ClassifyMigratability(context.Background(), kc, testNS, testLease)
	if err == nil || !strings.Contains(err.Error(), "dep1") {
		t.Fatalf("err = %v, want the corrupt placement annotation named rather than a quiet seamless verdict", err)
	}
}
