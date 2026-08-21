package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

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
			Workload:    &v1alpha1.WorkloadRef{Namespace: "batch"},
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
			CreatedAt:  &moment,
		}},
		MigratedWorkloads: []string{"batch/worker"},
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
	response := get(t, server, "/")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "No capacity lease exists") {
		t.Error("the empty cluster is not reported")
	}
}

func TestLeaseListRendersTheClusterLeases(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	now := time.Now()
	createLease(t, leaseFixture("batch-run"), activeStatus(now))

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{"batch-run", "nbg1", "Active", "cx22"} {
		if !strings.Contains(body, want) {
			t.Errorf("the lease list omits %q", want)
		}
	}
}

func TestLeaseFragmentCarriesNoPageShell(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("fragment-run"), activeStatus(time.Now()))

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/fragments/leases")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if !strings.Contains(body, "fragment-run") {
		t.Error("the fragment omits the lease")
	}
	if strings.Contains(body, "<html") {
		t.Error("the fragment carries a page shell, want the table alone")
	}
}

func TestLeaseDetailRendersStatusConditionsAndInstances(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("detail-run"), activeStatus(time.Now()))

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/leases/detail-run")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{"detail-run", v1alpha1.ConditionWatchdogArmed, "burst-0", "hcloud://42", "batch/worker"} {
		if !strings.Contains(body, want) {
			t.Errorf("the detail view omits %q", want)
		}
	}
}

func TestLeaseDetailReportsAMissingLease(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, "/leases/absent-run")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if !strings.Contains(response.Body.String(), "absent-run") {
		t.Error("the missing lease is not named")
	}
}

func TestLeaseViewsReportAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	for name, target := range map[string]string{
		"list":     "/",
		"detail":   "/leases/batch-run",
		"fragment": "/fragments/leases",
	} {
		t.Run(name, func(t *testing.T) {
			if response := get(t, server, target); response.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want %d", response.Code, http.StatusBadGateway)
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

	row := newLeaseRow(lease, now)

	for field, got := range map[string]string{
		"region":     row.Region,
		"phase":      row.Phase,
		"expires":    row.Expires,
		"ready":      row.Ready,
		"armed":      row.Armed,
		"age":        row.Age,
		"type":       row.Type,
		"readyAt":    row.ReadyAt,
		"releasedAt": row.ReleasedAt,
	} {
		want := map[string]string{
			"region":     "nbg1",
			"phase":      string(v1alpha1.LeasePhaseActive),
			"expires":    "90m",
			"ready":      string(metav1.ConditionTrue),
			"armed":      string(metav1.ConditionTrue),
			"age":        "90m",
			"type":       "cx22",
			"readyAt":    "30m",
			"releasedAt": absent,
		}[field]
		if got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	if row.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", row.Replicas)
	}
}

func TestLeaseRowReportsAnExpiredLease(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("expired-run")
	lease.Status.ExpiresAt = &metav1.Time{Time: now.Add(-time.Minute)}

	if row := newLeaseRow(lease, now); row.Expires != expired {
		t.Errorf("expires = %q, want %q", row.Expires, expired)
	}
}

func TestLeaseDetailRendersAPendingLease(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	lease := leaseFixture("pending-run")
	lease.Spec.Workload = nil

	detail := newLeaseDetail(lease, now)

	if detail.Workload != absent {
		t.Errorf("workload = %q, want %q", detail.Workload, absent)
	}
	if detail.ExpiresAt != absent {
		t.Errorf("expiresAt = %q, want %q", detail.ExpiresAt, absent)
	}
	if len(detail.Conditions) != 0 || len(detail.Instances) != 0 {
		t.Error("a pending lease carries conditions or instances, want neither")
	}
}
