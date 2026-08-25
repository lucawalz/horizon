package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/k8s"
)

func quantity(value string) *resource.Quantity {
	parsed := resource.MustParse(value)
	return &parsed
}

func validLease(t *testing.T) *v1alpha1.CapacityLease {
	t.Helper()
	return &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: objectName(t)},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: "hetzner",
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    1,
			Duration:    metav1.Duration{Duration: time.Hour},
		},
	}
}

func TestCapacityLeaseAcceptsAValidSpec(t *testing.T) {
	c := apiServerClient(t)
	assertCreate(t, c, validLease(t), false)
}

func TestCapacityLeaseReplicasBounds(t *testing.T) {
	tests := []struct {
		name         string
		replicas     int32
		wantRejected bool
	}{
		{"zero", 0, true},
		{"one", 1, false},
		{"eight", 8, false},
		{"nine", 9, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			lease.Spec.Replicas = tc.replicas
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseDurationBounds(t *testing.T) {
	tests := []struct {
		name         string
		duration     time.Duration
		wantRejected bool
	}{
		{"four minutes", 4 * time.Minute, true},
		{"five minutes", 5 * time.Minute, false},
		{"eight hours", 8 * time.Hour, false},
		{"nine hours", 9 * time.Hour, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			lease.Spec.Duration = metav1.Duration{Duration: tc.duration}
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseTeardownGraceBounds(t *testing.T) {
	tests := []struct {
		name         string
		grace        time.Duration
		wantRejected bool
	}{
		{"zero", 0, false},
		{"two minutes", 2 * time.Minute, false},
		{"fifteen minutes", 15 * time.Minute, false},
		{"just over fifteen minutes", 15*time.Minute + time.Millisecond, true},
		{"one hour", time.Hour, true},
		{"negative", -time.Second, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			lease.Spec.TeardownGrace = &metav1.Duration{Duration: tc.grace}
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseRejectsEmptyStringFields(t *testing.T) {
	tests := []struct {
		name  string
		blank func(*v1alpha1.CapacityLeaseSpec)
	}{
		{"providerRef", func(s *v1alpha1.CapacityLeaseSpec) { s.ProviderRef = "" }},
		{"region", func(s *v1alpha1.CapacityLeaseSpec) { s.Region = "" }},
		{"size", func(s *v1alpha1.CapacityLeaseSpec) { s.Size = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			tc.blank(&lease.Spec)
			assertCreate(t, c, lease, true)
		})
	}
}

func requiring(mutators ...func(*v1alpha1.SizeRequirements)) func(*v1alpha1.CapacityLeaseSpec) {
	required := &v1alpha1.SizeRequirements{MinCPU: 2, Architecture: v1alpha1.ArchitectureX86}
	for _, mutate := range mutators {
		mutate(required)
	}
	return func(spec *v1alpha1.CapacityLeaseSpec) {
		spec.Size = ""
		spec.Requirements = required
	}
}

func TestCapacityLeaseTakesExactlyOneOfSizeAndRequirements(t *testing.T) {
	tests := []struct {
		name         string
		sizing       func(*v1alpha1.CapacityLeaseSpec)
		wantRejected bool
	}{
		{"size alone", func(*v1alpha1.CapacityLeaseSpec) {}, false},
		{"requirements alone", requiring(), false},
		{"both", func(spec *v1alpha1.CapacityLeaseSpec) {
			spec.Requirements = &v1alpha1.SizeRequirements{MinCPU: 2, Architecture: v1alpha1.ArchitectureX86}
		}, true},
		{"neither", func(spec *v1alpha1.CapacityLeaseSpec) { spec.Size = "" }, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			tc.sizing(&lease.Spec)
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseRequirementsBounds(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*v1alpha1.SizeRequirements)
		wantRejected bool
	}{
		{"zero cores", func(r *v1alpha1.SizeRequirements) { r.MinCPU = 0 }, true},
		{"one core", func(r *v1alpha1.SizeRequirements) { r.MinCPU = 1 }, false},
		{"sixty four cores", func(r *v1alpha1.SizeRequirements) { r.MinCPU = 64 }, false},
		{"sixty five cores", func(r *v1alpha1.SizeRequirements) { r.MinCPU = 65 }, true},
		{"no architecture", func(r *v1alpha1.SizeRequirements) { r.Architecture = "" }, true},
		{"arm architecture", func(r *v1alpha1.SizeRequirements) { r.Architecture = v1alpha1.ArchitectureARM }, false},
		{"unknown architecture", func(r *v1alpha1.SizeRequirements) { r.Architecture = "risc" }, true},
		{"dedicated cpu type", func(r *v1alpha1.SizeRequirements) { r.CPUType = v1alpha1.CPUTypeDedicated }, false},
		{"unknown cpu type", func(r *v1alpha1.SizeRequirements) { r.CPUType = "burstable" }, true},
		{"price per core strategy", func(r *v1alpha1.SizeRequirements) {
			r.Strategy = v1alpha1.StrategyLowestPricePerCore
		}, false},
		{"unknown strategy", func(r *v1alpha1.SizeRequirements) { r.Strategy = "Cheapest" }, true},
		{"memory floor", func(r *v1alpha1.SizeRequirements) { r.MinMemory = quantity("8Gi") }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			requiring(tc.mutate)(&lease.Spec)
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseDefaultsTheSelectionStrategy(t *testing.T) {
	c := apiServerClient(t)
	lease := validLease(t)
	requiring()(&lease.Spec)
	assertCreate(t, c, lease, false)

	if got := lease.Spec.Requirements.Strategy; got != v1alpha1.StrategyLowestPrice {
		t.Errorf("strategy defaulted to %q, want %q", got, v1alpha1.StrategyLowestPrice)
	}
}

func targeting(mode v1alpha1.WorkloadMode, burstReplicas *int32) func(*v1alpha1.CapacityLeaseSpec) {
	return func(spec *v1alpha1.CapacityLeaseSpec) {
		spec.Workload = &v1alpha1.WorkloadRef{
			Namespaces:    []string{testWorkloadNS},
			Mode:          mode,
			BurstReplicas: burstReplicas,
		}
	}
}

func TestCapacityLeaseWorkloadModePairsWithBurstReplicas(t *testing.T) {
	tests := []struct {
		name         string
		target       func(*v1alpha1.CapacityLeaseSpec)
		wantRejected bool
	}{
		{"move without a pod count", targeting(v1alpha1.WorkloadModeMove, nil), false},
		{"move with a pod count", targeting(v1alpha1.WorkloadModeMove, int32Ptr(2)), true},
		{"replicate with a pod count", targeting(v1alpha1.WorkloadModeReplicate, int32Ptr(2)), false},
		{"replicate without a pod count", targeting(v1alpha1.WorkloadModeReplicate, nil), true},
		{"replicate with no pods at all", targeting(v1alpha1.WorkloadModeReplicate, int32Ptr(0)), true},
		{"an unknown mode", targeting("clone", nil), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			tc.target(&lease.Spec)
			assertCreate(t, c, lease, tc.wantRejected)
		})
	}
}

func TestCapacityLeaseDefaultsTheWorkloadModeToMove(t *testing.T) {
	c := apiServerClient(t)
	lease := validLease(t)
	lease.Spec.Workload = &v1alpha1.WorkloadRef{Namespaces: []string{testWorkloadNS}}
	assertCreate(t, c, lease, false)

	if got := lease.Spec.Workload.Mode; got != v1alpha1.WorkloadModeMove {
		t.Errorf("mode defaulted to %q, want %q", got, v1alpha1.WorkloadModeMove)
	}
}

func TestCapacityLeaseRefusesEveryChangeToWhatItAlreadyHolds(t *testing.T) {
	tests := []struct {
		name         string
		created      func(*v1alpha1.CapacityLeaseSpec)
		change       func(*v1alpha1.CapacityLeaseSpec)
		wantRejected bool
	}{
		{"providerRef", nil, func(s *v1alpha1.CapacityLeaseSpec) { s.ProviderRef = "other" }, true},
		{"region", nil, func(s *v1alpha1.CapacityLeaseSpec) { s.Region = "hel1" }, true},
		{"size", nil, func(s *v1alpha1.CapacityLeaseSpec) { s.Size = "cx32" }, true},
		{"size for requirements", nil, requiring(), true},
		{"requirements", requiring(), func(s *v1alpha1.CapacityLeaseSpec) { s.Requirements.MinCPU = 8 }, true},
		{"requirements strategy", requiring(), func(s *v1alpha1.CapacityLeaseSpec) {
			s.Requirements.Strategy = v1alpha1.StrategyLowestPricePerCore
		}, true},
		{"requirements for size", requiring(), func(s *v1alpha1.CapacityLeaseSpec) {
			s.Requirements = nil
			s.Size = "cx22"
		}, true},
		{"workload mode", targeting(v1alpha1.WorkloadModeMove, nil), func(s *v1alpha1.CapacityLeaseSpec) {
			s.Workload.Mode = v1alpha1.WorkloadModeReplicate
			s.Workload.BurstReplicas = int32Ptr(2)
		}, true},
		{"workload namespaces", targeting(v1alpha1.WorkloadModeMove, nil), func(s *v1alpha1.CapacityLeaseSpec) {
			s.Workload.Namespaces = []string{"other"}
		}, false},
		{"replicas", nil, func(s *v1alpha1.CapacityLeaseSpec) { s.Replicas = 3 }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			if tc.created != nil {
				tc.created(&lease.Spec)
			}
			assertCreate(t, c, lease, false)

			tc.change(&lease.Spec)
			assertUpdate(t, c, lease, tc.wantRejected)
		})
	}
}

func classifiedWorkloadRefs(t *testing.T) []string {
	t.Helper()
	targets, err := k8s.NewNamespaceSet([]string{"team-a", "team-b"})
	if err != nil {
		t.Fatalf("NewNamespaceSet: %v", err)
	}
	kube := k8sfake.NewSimpleClientset(
		selectedDeployment("team-a", "api"),
		selectedDeployment("team-b", "api"),
	)
	assessments, err := k8s.ClassifyMigratability(t.Context(), kube, targets, k8s.LeaseIdentity{UID: "uid", Name: "lease"})
	if err != nil {
		t.Fatalf("ClassifyMigratability: %v", err)
	}
	var refs []string
	for _, assessment := range assessments {
		refs = append(refs, assessment.Workload)
	}
	return refs
}

func selectedDeployment(namespace, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
		},
	}
}

func TestCapacityLeaseStatusRefusesTwoWarningsUnderOneWorkloadKey(t *testing.T) {
	tests := []struct {
		name         string
		workloads    func(*testing.T) []string
		wantRejected bool
	}{
		// the references come from the producer, so a reference that stopped naming its namespace is refused here rather than assumed distinct
		{"one name per namespace", classifiedWorkloadRefs, false},
		{"one name unqualified", func(*testing.T) []string { return []string{"deployment/api", "deployment/api"} }, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := apiServerClient(t)
			lease := validLease(t)
			assertCreate(t, c, lease, false)

			for _, workload := range tc.workloads(t) {
				lease.Status.MigrationWarnings = append(lease.Status.MigrationWarnings,
					v1alpha1.MigrationWarning{Workload: workload, Reasons: []string{k8s.ReasonRecreateStrategy}})
			}

			err := c.Status().Update(t.Context(), lease)
			switch {
			case tc.wantRejected && err == nil:
				t.Fatal("the apiserver accepted duplicate workload keys, so the list-map key carries no constraint")
			case !tc.wantRejected && err != nil:
				t.Fatalf("the apiserver rejected one workload name per namespace: %v", err)
			}
		})
	}
}
