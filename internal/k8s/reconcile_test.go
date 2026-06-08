package k8s_test

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func burstAffinity(ns string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      k8s.NodeAffinityLabelKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{ns},
					}},
				}},
			},
		},
	}
}

func readyNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestReconcileStrandedAffinity_StripsWhenNodeGone(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: burstAffinity("sentio-systems")},
		}},
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts1", Namespace: "sentio-systems"},
		Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: burstAffinity("sentio-systems")},
		}},
	}
	homelab := readyNode("homelab-1", nil)

	kc := fake.NewSimpleClientset(dep, sts, homelab)

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	d, _ := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity != nil && d.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Errorf("deployment nodeAffinity not stripped: %+v", d.Spec.Template.Spec.Affinity.NodeAffinity)
	}
	s, _ := kc.AppsV1().StatefulSets("sentio-systems").Get(context.Background(), "sts1", metav1.GetOptions{})
	if s.Spec.Template.Spec.Affinity != nil && s.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Errorf("statefulset nodeAffinity not stripped: %+v", s.Spec.Template.Spec.Affinity.NodeAffinity)
	}
}

func TestReconcileStrandedAffinity_KeepsWhenNodeLive(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: burstAffinity("sentio-systems")},
		}},
	}
	burst := readyNode("horizon-burst-abcd", map[string]string{k8s.NodeAffinityLabelKey: "sentio-systems"})

	kc := fake.NewSimpleClientset(dep, burst)

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	d, _ := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity == nil || d.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		t.Error("nodeAffinity stripped even though a live ready node still carries the label")
	}
}

func TestReconcileStrandedAffinity_PreservesOtherAffinity(t *testing.T) {
	aff := burstAffinity("sentio-systems")
	aff.PodAntiAffinity = &corev1.PodAntiAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
			Weight:          100,
			PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
		}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: aff},
		}},
	}

	kc := fake.NewSimpleClientset(dep, readyNode("homelab-1", nil))

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	d, _ := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity == nil || d.Spec.Template.Spec.Affinity.PodAntiAffinity == nil {
		t.Fatal("podAntiAffinity was wrongly removed")
	}
	if d.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		t.Error("burst nodeAffinity should have been stripped")
	}
}

func TestReconcileStrandedAffinity_PreservesNonBurstNodeAffinityTerm(t *testing.T) {
	aff := burstAffinity("sentio-systems")
	aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(
		aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
		corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: "disktype", Operator: corev1.NodeSelectorOpIn, Values: []string{"ssd"},
		}}},
	)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: aff},
		}},
	}

	kc := fake.NewSimpleClientset(dep, readyNode("homelab-1", nil))

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	d, _ := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "dep1", metav1.GetOptions{})
	na := d.Spec.Template.Spec.Affinity.NodeAffinity
	if na == nil || na.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatal("non-burst nodeAffinity term was wrongly removed")
	}
	terms := na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || terms[0].MatchExpressions[0].Key != "disktype" {
		t.Errorf("expected only the disktype term to remain, got %+v", terms)
	}
}

func TestReconcileStrandedAffinity_KeepsWhenNodePresentButNotReady(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: burstAffinity("sentio-systems")},
		}},
	}
	notReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "horizon-burst-dead", Labels: map[string]string{k8s.NodeAffinityLabelKey: "sentio-systems"}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
		},
	}

	kc := fake.NewSimpleClientset(dep, notReady)

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	d, _ := kc.AppsV1().Deployments("sentio-systems").Get(context.Background(), "dep1", metav1.GetOptions{})
	if d.Spec.Template.Spec.Affinity == nil || d.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		t.Error("nodeAffinity must NOT be stripped while a node carrying the label still exists, even if momentarily NotReady")
	}
}

func TestReconcileStrandedAffinity_NoOpWithoutBurstAffinity(t *testing.T) {
	plain := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight:          1,
				PodAffinityTerm: corev1.PodAffinityTerm{TopologyKey: "kubernetes.io/hostname"},
			}},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "sentio-systems"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Affinity: plain},
		}},
	}

	kc := fake.NewSimpleClientset(dep, readyNode("homelab-1", nil))

	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "sentio-systems"); err != nil {
		t.Fatalf("ReconcileStrandedAffinity: %v", err)
	}

	for _, a := range kc.Actions() {
		if a.GetVerb() == "patch" {
			t.Errorf("unexpected patch issued when no burst affinity present: %v", a)
		}
	}
}

func TestReconcileStrandedAffinity_RejectsBadNamespace(t *testing.T) {
	kc := fake.NewSimpleClientset()
	if err := k8s.ReconcileStrandedAffinity(context.Background(), kc, "Bad_NS"); err == nil {
		t.Fatal("expected error for invalid namespace")
	}
}
