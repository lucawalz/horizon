package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

func leaseFixture(name string) *v1alpha1.CapacityLease {
	return &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: "hetzner",
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    2,
			Duration:    metav1.Duration{Duration: 2 * time.Hour},
			Workload:    &v1alpha1.WorkloadRef{Namespaces: []string{"batch"}},
		},
	}
}

func activeStatus(now time.Time) v1alpha1.CapacityLeaseStatus {
	moment := metav1.NewTime(now)
	return v1alpha1.CapacityLeaseStatus{
		ObservedGeneration: 1,
		Phase:              v1alpha1.LeasePhaseActive,
		AcceptedAt:         &moment,
		ExpiresAt:          &metav1.Time{Time: now.Add(2 * time.Hour)},
		ReadyAt:            &moment,
		ProviderConfig:     "hetzner",
		Region:             "nbg1",
		InstanceType:       "cx22",
		Instances: []v1alpha1.InstanceStatus{{
			Name:       "burst-0",
			ProviderID: "hcloud://42",
			NodeName:   "burst-0",
			Phase:      v1alpha1.InstancePhaseJoined,
			Stage:      v1alpha1.InstanceStageReady,
			CreatedAt:  &moment,
		}},
		MigratedWorkloads: []string{"batch/worker"},
		MigrationWarnings: []v1alpha1.MigrationWarning{{
			Workload: "batch/worker",
			Reasons:  []string{"RecreateStrategy"},
		}},
		Conditions: []metav1.Condition{
			condition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue, now),
			condition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionTrue, now),
		},
	}
}

func condition(name string, status metav1.ConditionStatus, now time.Time) metav1.Condition {
	return metav1.Condition{
		Type:               name,
		Status:             status,
		Reason:             "Observed",
		Message:            "observed by the controller",
		LastTransitionTime: metav1.NewTime(now),
		ObservedGeneration: 1,
	}
}

func TestLeaseListRendersAnEmptyCluster(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/leases")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if leases := decodeBody[leaseListResponse](t, response).Leases; len(leases) != 0 {
		t.Errorf("leases = %d, want none", len(leases))
	}
	if body := response.Body.String(); !strings.Contains(body, `"leases":[]`) {
		t.Errorf("the empty cluster encoded as %s, want an empty list rather than null", body)
	}
}

func TestLeaseListRendersTheClusterLeases(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	now := time.Now()
	createLease(t, leaseFixture("batch-run"), activeStatus(now))

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/leases")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	leases := decodeBody[leaseListResponse](t, response).Leases
	if len(leases) != 1 {
		t.Fatalf("leases = %d, want 1", len(leases))
	}

	summary := leases[0]
	if summary.Name != "batch-run" {
		t.Errorf("name = %q, want %q", summary.Name, "batch-run")
	}
	if summary.Region != "nbg1" {
		t.Errorf("region = %q, want %q", summary.Region, "nbg1")
	}
	if phase := present(t, "phase", summary.Phase); phase != v1alpha1.LeasePhaseActive {
		t.Errorf("phase = %q, want %q", phase, v1alpha1.LeasePhaseActive)
	}
	if instanceType := present(t, "instanceType", summary.InstanceType); instanceType != "cx22" {
		t.Errorf("instanceType = %q, want %q", instanceType, "cx22")
	}
}

func TestLeaseDetailRendersStatusConditionsAndInstances(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("detail-run"), activeStatus(time.Now()))

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/leases/detail-run")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	detail := decodeBody[leaseDetailResponse](t, response)
	if detail.Summary.Name != "detail-run" {
		t.Errorf("summary.name = %q, want %q", detail.Summary.Name, "detail-run")
	}

	armed := conditionEntryNamed(detail.Conditions, v1alpha1.ConditionWatchdogArmed)
	if armed == nil {
		t.Fatalf("the conditions omit %q", v1alpha1.ConditionWatchdogArmed)
	}
	if armed.Status != metav1.ConditionTrue {
		t.Errorf("%s status = %q, want %q", armed.Type, armed.Status, metav1.ConditionTrue)
	}

	if len(detail.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(detail.Instances))
	}
	instance := detail.Instances[0]
	if instance.Name != "burst-0" {
		t.Errorf("instance name = %q, want %q", instance.Name, "burst-0")
	}
	if providerID := present(t, "providerID", instance.ProviderID); providerID != "hcloud://42" {
		t.Errorf("providerID = %q, want %q", providerID, "hcloud://42")
	}
	if stage := present(t, "stage", instance.Stage); stage != v1alpha1.InstanceStageReady {
		t.Errorf("stage = %q, want %q", stage, v1alpha1.InstanceStageReady)
	}

	if len(detail.MigratedWorkloads) != 1 || detail.MigratedWorkloads[0] != "batch/worker" {
		t.Errorf("migratedWorkloads = %v, want [batch/worker]", detail.MigratedWorkloads)
	}

	if len(detail.MigrationWarnings) != 1 {
		t.Fatalf("migrationWarnings = %+v, want one entry", detail.MigrationWarnings)
	}
	warning := detail.MigrationWarnings[0]
	if warning.Workload != "batch/worker" || len(warning.Reasons) != 1 || warning.Reasons[0] != "RecreateStrategy" {
		t.Errorf("migrationWarnings[0] = %+v, want batch/worker flagged as RecreateStrategy", warning)
	}
}

func latchedInstance(name string, phase v1alpha1.InstancePhase, backstop time.Time) v1alpha1.InstanceStatus {
	return v1alpha1.InstanceStatus{
		Name:       name,
		Phase:      phase,
		BackstopAt: &metav1.Time{Time: backstop},
	}
}

func TestLeaseDetailCarriesTheEarliestBackstopStillHeld(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	now := time.Now()
	status := activeStatus(now)
	status.Instances = []v1alpha1.InstanceStatus{
		latchedInstance("burst-0", v1alpha1.InstancePhaseJoined, now.Add(6*time.Hour)),
		latchedInstance("burst-1", v1alpha1.InstancePhaseCreated, now.Add(5*time.Hour)),
		latchedInstance("burst-2", v1alpha1.InstancePhaseReleased, now.Add(time.Hour)),
	}
	createLease(t, leaseFixture("ceiling-run"), status)

	response := get(t, newTestServer(t, testEnv.Client, AbsentCatalogue()), leaseEndpoint("ceiling-run"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}

	detail := decodeBody[leaseDetailResponse](t, response)
	backstop := parseInstant(t, "backstopAt", present(t, "backstopAt", detail.BackstopAt))
	if want := now.Add(5 * time.Hour).Truncate(time.Second); !backstop.Equal(want) {
		t.Errorf("backstopAt = %s, want %s", backstop, want)
	}
}

func TestLeaseDetailReportsNoBackstopWhereNoneIsLatched(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("unlatched-run"), activeStatus(time.Now()))

	response := get(t, newTestServer(t, testEnv.Client, AbsentCatalogue()), leaseEndpoint("unlatched-run"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	if backstop := decodeBody[leaseDetailResponse](t, response).BackstopAt; backstop != nil {
		t.Errorf("backstopAt = %q, want it absent", *backstop)
	}
}

func conditionEntryNamed(conditions []conditionEntry, name string) *conditionEntry {
	for i := range conditions {
		if conditions[i].Type == name {
			return &conditions[i]
		}
	}
	return nil
}

func TestLeaseDetailReportsAMissingLease(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/api/leases/absent-run")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	failure := decodeBody[apiError](t, response)
	if failure.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", failure.Status, http.StatusNotFound)
	}
	if want := http.StatusText(http.StatusNotFound); failure.Title != want {
		t.Errorf("title = %q, want %q", failure.Title, want)
	}
	if !strings.Contains(failure.Detail, "absent-run") {
		t.Errorf("detail = %q, want the missing lease named", failure.Detail)
	}
}

func TestLeaseViewsReportAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	for name, target := range map[string]string{
		"list":   "/api/leases",
		"detail": "/api/leases/batch-run",
	} {
		t.Run(name, func(t *testing.T) {
			response := get(t, server, target)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
			}
			if failure := decodeBody[apiError](t, response); failure.Status != http.StatusBadGateway {
				t.Errorf("body status = %d, want %d", failure.Status, http.StatusBadGateway)
			}
		})
	}
}

func TestLeaseRowFollowsThePrinterColumns(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("row-run")
	lease.CreationTimestamp = metav1.NewTime(now.Add(-90 * time.Minute))
	lease.Status = activeStatus(now.Add(-30 * time.Minute))
	lease.Status.ReleasedAt = nil
	lease.Status.Conditions = []metav1.Condition{
		condition(v1alpha1.ConditionInstancesReady, metav1.ConditionTrue, now),
		condition(v1alpha1.ConditionWatchdogArmed, metav1.ConditionFalse, now),
	}

	summary := newLeaseSummary(lease)

	for field, got := range map[string]string{
		"region":       summary.Region,
		"phase":        string(present(t, "phase", summary.Phase)),
		"expiresAt":    present(t, "expiresAt", summary.ExpiresAt),
		"ready":        string(present(t, "ready", summary.Ready)),
		"armed":        string(present(t, "armed", summary.Armed)),
		"createdAt":    summary.CreatedAt,
		"instanceType": present(t, "instanceType", summary.InstanceType),
		"readyAt":      present(t, "readyAt", summary.ReadyAt),
	} {
		want := map[string]string{
			"region":       "nbg1",
			"phase":        string(v1alpha1.LeasePhaseActive),
			"expiresAt":    now.Add(90 * time.Minute).Format(time.RFC3339),
			"ready":        string(metav1.ConditionTrue),
			"armed":        string(metav1.ConditionFalse),
			"createdAt":    now.Add(-90 * time.Minute).Format(time.RFC3339),
			"instanceType": "cx22",
			"readyAt":      now.Add(-30 * time.Minute).Format(time.RFC3339),
		}[field]
		if got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	if summary.ReleasedAt != nil {
		t.Errorf("releasedAt = %q, want null", *summary.ReleasedAt)
	}
	if summary.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", summary.Replicas)
	}
}

func TestLeaseRowReportsAnExpiredLease(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("expired-run")
	lease.Status.ExpiresAt = &metav1.Time{Time: now.Add(-time.Minute)}

	response := newLeaseListResponse([]v1alpha1.CapacityLease{*lease}, now)
	if len(response.Leases) != 1 {
		t.Fatalf("leases = %d, want 1", len(response.Leases))
	}

	expiresAt := parseInstant(t, "expiresAt", present(t, "expiresAt", response.Leases[0].ExpiresAt))
	observedAt := parseInstant(t, "observedAt", response.ObservedAt)
	if !expiresAt.Before(observedAt) {
		t.Errorf("expiresAt = %s, want an instant before observedAt %s", expiresAt, observedAt)
	}
}

func TestLeaseDetailRendersAPendingLease(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("pending-run")
	lease.Spec.Workload = nil

	detail := newLeaseDetailResponse(lease, now)

	if len(detail.WorkloadNamespaces) != 0 {
		t.Errorf("workloadNamespaces = %v, want an empty list", detail.WorkloadNamespaces)
	}
	if detail.WorkloadSelector != nil {
		t.Errorf("workloadSelector = %q, want null", *detail.WorkloadSelector)
	}
	if detail.Summary.ExpiresAt != nil {
		t.Errorf("summary.expiresAt = %q, want null", *detail.Summary.ExpiresAt)
	}
	if detail.Conditions == nil || len(detail.Conditions) != 0 {
		t.Errorf("conditions = %v, want an empty list rather than null", detail.Conditions)
	}
	if detail.Instances == nil || len(detail.Instances) != 0 {
		t.Errorf("instances = %v, want an empty list rather than null", detail.Instances)
	}
	if detail.MigratedWorkloads == nil || len(detail.MigratedWorkloads) != 0 {
		t.Errorf("migratedWorkloads = %v, want an empty list rather than null", detail.MigratedWorkloads)
	}
	if detail.MigrationWarnings == nil || len(detail.MigrationWarnings) != 0 {
		t.Errorf("migrationWarnings = %v, want an empty list rather than null", detail.MigrationWarnings)
	}
}

func TestLeaseDetailExposesTheSizeRequirements(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("sized-run")
	lease.Spec.Size = ""
	lease.Spec.Requirements = &v1alpha1.SizeRequirements{
		MinCPU:       4,
		MinMemory:    ptr(resource.MustParse("8Gi")),
		Architecture: v1alpha1.ArchitectureARM,
		CPUType:      v1alpha1.CPUTypeDedicated,
		Strategy:     v1alpha1.StrategyLowestPricePerCore,
	}

	detail := newLeaseDetailResponse(lease, now)

	if detail.Size != nil {
		t.Errorf("size = %q, want null", *detail.Size)
	}
	requirements := present(t, "requirements", detail.Requirements)
	if requirements.MinCPU != 4 {
		t.Errorf("minCPU = %d, want 4", requirements.MinCPU)
	}
	if minMemory := present(t, "minMemory", requirements.MinMemory); minMemory != "8Gi" {
		t.Errorf("minMemory = %q, want %q", minMemory, "8Gi")
	}
	if requirements.Architecture != v1alpha1.ArchitectureARM {
		t.Errorf("architecture = %q, want %q", requirements.Architecture, v1alpha1.ArchitectureARM)
	}
	if cpuType := present(t, "cpuType", requirements.CPUType); cpuType != v1alpha1.CPUTypeDedicated {
		t.Errorf("cpuType = %q, want %q", cpuType, v1alpha1.CPUTypeDedicated)
	}
	if strategy := present(t, "strategy", requirements.Strategy); strategy != v1alpha1.StrategyLowestPricePerCore {
		t.Errorf("strategy = %q, want %q", strategy, v1alpha1.StrategyLowestPricePerCore)
	}
}

func TestLeaseDetailCarriesDurationsInSeconds(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("timed-run")
	lease.Spec.Duration = metav1.Duration{Duration: 2 * time.Hour}
	lease.Spec.TeardownGrace = &metav1.Duration{Duration: 90 * time.Second}

	detail := newLeaseDetailResponse(lease, now)

	if detail.DurationSeconds != 7200 {
		t.Errorf("durationSeconds = %d, want 7200 for two hours", detail.DurationSeconds)
	}
	if grace := present(t, "teardownGraceSeconds", detail.TeardownGraceSeconds); grace != 90 {
		t.Errorf("teardownGraceSeconds = %d, want 90", grace)
	}
}

func TestLeaseDetailRendersWhyTheInstanceTypeWasChosen(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("chosen-run")
	lease.Status.Selection = &v1alpha1.SelectionStatus{
		Strategy:   v1alpha1.StrategyLowestPricePerCore,
		Chosen:     "cx23",
		HourlyRate: "0.0080",
		Currency:   "EUR",
		RunnerUp:   "cpx21",
		Offered:    31,
		Qualified:  4,
		Rejected: []v1alpha1.RejectedCandidates{
			{Reason: "TooFewCores", Count: 19},
			{Reason: "TooLittleMemory", Count: 8},
		},
		DecidedAt: metav1.NewTime(now),
	}

	selection := present(t, "selection", newLeaseDetailResponse(lease, now).Selection)

	if selection.Strategy != v1alpha1.StrategyLowestPricePerCore {
		t.Errorf("strategy = %q, want %q", selection.Strategy, v1alpha1.StrategyLowestPricePerCore)
	}
	if selection.Chosen != "cx23" {
		t.Errorf("chosen = %q, want %q", selection.Chosen, "cx23")
	}
	if rate := present(t, "hourlyRate", selection.HourlyRate); rate != "0.0080" {
		t.Errorf("hourlyRate = %q, want %q", rate, "0.0080")
	}
	if currency := present(t, "currency", selection.Currency); currency != "EUR" {
		t.Errorf("currency = %q, want %q", currency, "EUR")
	}
	if runnerUp := present(t, "runnerUp", selection.RunnerUp); runnerUp != "cpx21" {
		t.Errorf("runnerUp = %q, want %q", runnerUp, "cpx21")
	}
	if selection.Offered != 31 || selection.Qualified != 4 {
		t.Errorf("offered = %d and qualified = %d, want 31 and 4", selection.Offered, selection.Qualified)
	}
	if len(selection.Rejected) != 2 {
		t.Fatalf("rejected reasons = %d, want 2", len(selection.Rejected))
	}
	if selection.Rejected[0].Reason != "TooFewCores" || selection.Rejected[0].Count != 19 {
		t.Errorf("first rejection = %+v, want 19 for TooFewCores", selection.Rejected[0])
	}
	if selection.DecidedAt != rfc3339(now) {
		t.Errorf("decidedAt = %q, want %q", selection.DecidedAt, rfc3339(now))
	}
}

// naming a machine type is not a policy decision, so a lease that named one has no reasoning to show
func TestLeaseDetailCarriesNoSelectionForANamedSize(t *testing.T) {
	detail := newLeaseDetailResponse(leaseFixture("named-run"), time.Now())

	if detail.Selection != nil {
		t.Errorf("selection = %+v, want null", *detail.Selection)
	}
}

func TestLeaseDetailEncodesTheWorkloadTargetSet(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("targeted-run")
	lease.Spec.Workload = &v1alpha1.WorkloadRef{
		Namespaces: []string{"team-a", "team-b"},
		Selector:   &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "batch"}},
	}

	encoded, err := json.Marshal(newLeaseDetailResponse(lease, now))
	if err != nil {
		t.Fatalf("marshal the detail: %v", err)
	}

	var decoded struct {
		WorkloadNamespaces []string `json:"workloadNamespaces"`
		WorkloadSelector   *string  `json:"workloadSelector"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode the detail: %v", err)
	}

	want := []string{"team-a", "team-b"}
	if !reflect.DeepEqual(decoded.WorkloadNamespaces, want) {
		t.Errorf("workloadNamespaces = %v, want %v", decoded.WorkloadNamespaces, want)
	}
	if decoded.WorkloadSelector == nil || *decoded.WorkloadSelector != "tier=batch" {
		t.Errorf("workloadSelector = %v, want %q", decoded.WorkloadSelector, "tier=batch")
	}
}
