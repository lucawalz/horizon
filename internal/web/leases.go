package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	leaseListTitle  = "horizon capacity leases"
	leaseReadFailed = "the capacity leases could not be read from the cluster"
)

type leaseRow struct {
	Name       string
	Replicas   int32
	Region     string
	Phase      string
	Expires    string
	Ready      string
	Armed      string
	Age        string
	Type       string
	ReadyAt    string
	ReleasedAt string
}

type leaseTable struct {
	Rows    []leaseRow
	Updated string
}

type conditionRow struct {
	Type    string
	Status  string
	Reason  string
	Message string
	Age     string
}

type instanceRow struct {
	Name       string
	ProviderID string
	NodeName   string
	Phase      string
	Age        string
	LastError  string
}

type leaseDetail struct {
	Row              leaseRow
	Provider         string
	Size             string
	Duration         string
	TeardownGrace    string
	Workload         string
	Migrated         []string
	AcceptedAt       string
	ExpiresAt        string
	ReadyAt          string
	ReleasedAt       string
	WatchdogDeadline string
	Generation       int64
	Conditions       []conditionRow
	Instances        []instanceRow
	Updated          string
}

func newLeaseRow(lease *v1alpha1.CapacityLease, now time.Time) leaseRow {
	return leaseRow{
		Name:       lease.Name,
		Replicas:   lease.Spec.Replicas,
		Region:     lease.Spec.Region,
		Phase:      text(string(lease.Status.Phase)),
		Expires:    remaining(lease.Status.ExpiresAt, now),
		Ready:      conditionStatus(lease.Status.Conditions, v1alpha1.ConditionInstancesReady),
		Armed:      conditionStatus(lease.Status.Conditions, v1alpha1.ConditionWatchdogArmed),
		Age:        age(&lease.CreationTimestamp, now),
		Type:       text(lease.Status.InstanceType),
		ReadyAt:    age(lease.Status.ReadyAt, now),
		ReleasedAt: age(lease.Status.ReleasedAt, now),
	}
}

func newLeaseTable(leases []v1alpha1.CapacityLease, now time.Time) leaseTable {
	rows := make([]leaseRow, 0, len(leases))
	for i := range leases {
		rows = append(rows, newLeaseRow(&leases[i], now))
	}
	return leaseTable{Rows: rows, Updated: now.Format(time.TimeOnly)}
}

func newLeaseDetail(lease *v1alpha1.CapacityLease, now time.Time) leaseDetail {
	return leaseDetail{
		Row:              newLeaseRow(lease, now),
		Provider:         lease.Spec.ProviderRef,
		Size:             lease.Spec.Size,
		Duration:         lease.Spec.Duration.Duration.String(),
		TeardownGrace:    teardownGrace(lease.Spec.TeardownGrace),
		Workload:         workload(lease.Spec.Workload),
		Migrated:         lease.Status.MigratedWorkloads,
		AcceptedAt:       stamp(lease.Status.AcceptedAt, now),
		ExpiresAt:        stamp(lease.Status.ExpiresAt, now),
		ReadyAt:          stamp(lease.Status.ReadyAt, now),
		ReleasedAt:       stamp(lease.Status.ReleasedAt, now),
		WatchdogDeadline: stamp(lease.Status.WatchdogDeadline, now),
		Generation:       lease.Status.ObservedGeneration,
		Conditions:       newConditionRows(lease.Status.Conditions, now),
		Instances:        newInstanceRows(lease.Status.Instances, now),
		Updated:          now.Format(time.TimeOnly),
	}
}

func newConditionRows(conditions []metav1.Condition, now time.Time) []conditionRow {
	rows := make([]conditionRow, 0, len(conditions))
	for i := range conditions {
		condition := &conditions[i]
		rows = append(rows, conditionRow{
			Type:    condition.Type,
			Status:  string(condition.Status),
			Reason:  text(condition.Reason),
			Message: text(condition.Message),
			Age:     age(&condition.LastTransitionTime, now),
		})
	}
	return rows
}

func newInstanceRows(instances []v1alpha1.InstanceStatus, now time.Time) []instanceRow {
	rows := make([]instanceRow, 0, len(instances))
	for i := range instances {
		instance := &instances[i]
		rows = append(rows, instanceRow{
			Name:       instance.Name,
			ProviderID: text(instance.ProviderID),
			NodeName:   text(instance.NodeName),
			Phase:      text(string(instance.Phase)),
			Age:        age(instance.CreatedAt, now),
			LastError:  text(instance.LastError),
		})
	}
	return rows
}

func teardownGrace(grace *metav1.Duration) string {
	if grace == nil {
		return absent
	}
	return grace.Duration.String()
}

func workload(ref *v1alpha1.WorkloadRef) string {
	if ref == nil {
		return absent
	}
	return ref.Namespace
}

func (s *Server) leaseList(block string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var leases v1alpha1.CapacityLeaseList
		if err := s.client.List(r.Context(), &leases); err != nil {
			slog.Error("list the capacity leases", "error", err)
			s.fail(w, block, http.StatusBadGateway, leaseReadFailed)
			return
		}
		s.render(w, leasesPage, block, http.StatusOK,
			payload(block, leaseListTitle, newLeaseTable(leases.Items, time.Now())))
	}
}

func (s *Server) leaseDetail(block string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var lease v1alpha1.CapacityLease
		if err := s.client.Get(r.Context(), client.ObjectKey{Name: name}, &lease); err != nil {
			if apierrors.IsNotFound(err) {
				s.fail(w, block, http.StatusNotFound,
					fmt.Sprintf("no capacity lease named %q exists in the cluster", name))
				return
			}
			slog.Error("read the capacity lease", "lease", name, "error", err)
			s.fail(w, block, http.StatusBadGateway, leaseReadFailed)
			return
		}
		s.render(w, leasePage, block, http.StatusOK,
			payload(block, "horizon lease "+lease.Name, newLeaseDetail(&lease, time.Now())))
	}
}
