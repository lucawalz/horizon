package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const leaseReadFailed = "the capacity leases could not be read from the cluster"

type leaseSummary struct {
	Name         string                  `json:"name"`
	Replicas     int32                   `json:"replicas"`
	Region       string                  `json:"region"`
	Phase        *v1alpha1.LeasePhase    `json:"phase"`
	ExpiresAt    *string                 `json:"expiresAt"`
	Ready        *metav1.ConditionStatus `json:"ready"`
	Armed        *metav1.ConditionStatus `json:"armed"`
	CreatedAt    string                  `json:"createdAt"`
	InstanceType *string                 `json:"instanceType"`
	ReadyAt      *string                 `json:"readyAt"`
	ReleasedAt   *string                 `json:"releasedAt"`
}

type leaseListResponse struct {
	Leases     []leaseSummary `json:"leases"`
	ObservedAt string         `json:"observedAt"`
}

type conditionEntry struct {
	Type               string                 `json:"type"`
	Status             metav1.ConditionStatus `json:"status"`
	Reason             *string                `json:"reason"`
	Message            *string                `json:"message"`
	LastTransitionTime string                 `json:"lastTransitionTime"`
}

type leaseInstance struct {
	Name       string                  `json:"name"`
	ProviderID *string                 `json:"providerID"`
	NodeName   *string                 `json:"nodeName"`
	Phase      v1alpha1.InstancePhase  `json:"phase"`
	Stage      *v1alpha1.InstanceStage `json:"stage"`
	CreatedAt  *string                 `json:"createdAt"`
	LastError  *string                 `json:"lastError"`
}

type leaseRequirements struct {
	MinCPU       int32                    `json:"minCPU"`
	MinMemory    *string                  `json:"minMemory"`
	Architecture v1alpha1.Architecture    `json:"architecture"`
	CPUType      *v1alpha1.CPUType        `json:"cpuType"`
	Strategy     *v1alpha1.SizingStrategy `json:"strategy"`
}

type rejectedCandidates struct {
	Reason string `json:"reason"`
	Count  int32  `json:"count"`
}

type leaseSelection struct {
	Strategy   v1alpha1.SizingStrategy `json:"strategy"`
	Chosen     string                  `json:"chosen"`
	HourlyRate *string                 `json:"hourlyRate"`
	Currency   *string                 `json:"currency"`
	RunnerUp   *string                 `json:"runnerUp"`
	Offered    int32                   `json:"offered"`
	Qualified  int32                   `json:"qualified"`
	Rejected   []rejectedCandidates    `json:"rejected"`
	DecidedAt  string                  `json:"decidedAt"`
}

type leaseDetailResponse struct {
	Summary              leaseSummary       `json:"summary"`
	ProviderRef          string             `json:"providerRef"`
	Size                 *string            `json:"size"`
	Requirements         *leaseRequirements `json:"requirements"`
	Selection            *leaseSelection    `json:"selection"`
	DurationSeconds      int64              `json:"durationSeconds"`
	TeardownGraceSeconds *int64             `json:"teardownGraceSeconds"`
	WorkloadNamespace    *string            `json:"workloadNamespace"`
	MigratedWorkloads    []string           `json:"migratedWorkloads"`
	AcceptedAt           *string            `json:"acceptedAt"`
	WatchdogDeadline     *string            `json:"watchdogDeadline"`
	ObservedGeneration   int64              `json:"observedGeneration"`
	Conditions           []conditionEntry   `json:"conditions"`
	Instances            []leaseInstance    `json:"instances"`
	ObservedAt           string             `json:"observedAt"`
}

func newLeaseSummary(lease *v1alpha1.CapacityLease) leaseSummary {
	return leaseSummary{
		Name:         lease.Name,
		Replicas:     lease.Spec.Replicas,
		Region:       lease.Spec.Region,
		Phase:        nullable(lease.Status.Phase),
		ExpiresAt:    instant(lease.Status.ExpiresAt),
		Ready:        conditionStatus(lease.Status.Conditions, v1alpha1.ConditionInstancesReady),
		Armed:        conditionStatus(lease.Status.Conditions, v1alpha1.ConditionWatchdogArmed),
		CreatedAt:    rfc3339(lease.CreationTimestamp.Time),
		InstanceType: nullable(lease.Status.InstanceType),
		ReadyAt:      instant(lease.Status.ReadyAt),
		ReleasedAt:   instant(lease.Status.ReleasedAt),
	}
}

func newLeaseListResponse(leases []v1alpha1.CapacityLease, now time.Time) leaseListResponse {
	summaries := make([]leaseSummary, 0, len(leases))
	for i := range leases {
		summaries = append(summaries, newLeaseSummary(&leases[i]))
	}
	return leaseListResponse{Leases: summaries, ObservedAt: rfc3339(now)}
}

func newLeaseDetailResponse(lease *v1alpha1.CapacityLease, now time.Time) leaseDetailResponse {
	return leaseDetailResponse{
		Summary:              newLeaseSummary(lease),
		ProviderRef:          lease.Spec.ProviderRef,
		Size:                 nullable(lease.Spec.Size),
		Requirements:         newLeaseRequirements(lease.Spec.Requirements),
		Selection:            newLeaseSelection(lease.Status.Selection),
		DurationSeconds:      seconds(lease.Spec.Duration.Duration),
		TeardownGraceSeconds: teardownGraceSeconds(lease.Spec.TeardownGrace),
		WorkloadNamespace:    workloadNamespace(lease.Spec.Workload),
		MigratedWorkloads:    orEmpty(lease.Status.MigratedWorkloads),
		AcceptedAt:           instant(lease.Status.AcceptedAt),
		WatchdogDeadline:     instant(lease.Status.WatchdogDeadline),
		ObservedGeneration:   lease.Status.ObservedGeneration,
		Conditions:           newConditionEntries(lease.Status.Conditions),
		Instances:            newLeaseInstances(lease.Status.Instances),
		ObservedAt:           rfc3339(now),
	}
}

func newLeaseRequirements(requirements *v1alpha1.SizeRequirements) *leaseRequirements {
	if requirements == nil {
		return nil
	}

	var minMemory *string
	if requirements.MinMemory != nil {
		minMemory = ptr(requirements.MinMemory.String())
	}
	return &leaseRequirements{
		MinCPU:       requirements.MinCPU,
		MinMemory:    minMemory,
		Architecture: requirements.Architecture,
		CPUType:      nullable(requirements.CPUType),
		Strategy:     nullable(requirements.Strategy),
	}
}

func newLeaseSelection(selection *v1alpha1.SelectionStatus) *leaseSelection {
	if selection == nil {
		return nil
	}

	rejected := make([]rejectedCandidates, 0, len(selection.Rejected))
	for _, candidates := range selection.Rejected {
		rejected = append(rejected, rejectedCandidates{Reason: candidates.Reason, Count: candidates.Count})
	}
	return &leaseSelection{
		Strategy:   selection.Strategy,
		Chosen:     selection.Chosen,
		HourlyRate: nullable(selection.HourlyRate),
		Currency:   nullable(selection.Currency),
		RunnerUp:   nullable(selection.RunnerUp),
		Offered:    selection.Offered,
		Qualified:  selection.Qualified,
		Rejected:   rejected,
		DecidedAt:  rfc3339(selection.DecidedAt.Time),
	}
}

func newConditionEntries(conditions []metav1.Condition) []conditionEntry {
	entries := make([]conditionEntry, 0, len(conditions))
	for i := range conditions {
		condition := &conditions[i]
		entries = append(entries, conditionEntry{
			Type:               condition.Type,
			Status:             condition.Status,
			Reason:             nullable(condition.Reason),
			Message:            nullable(condition.Message),
			LastTransitionTime: rfc3339(condition.LastTransitionTime.Time),
		})
	}
	return entries
}

func newLeaseInstances(instances []v1alpha1.InstanceStatus) []leaseInstance {
	entries := make([]leaseInstance, 0, len(instances))
	for i := range instances {
		instance := &instances[i]
		entries = append(entries, leaseInstance{
			Name:       instance.Name,
			ProviderID: nullable(instance.ProviderID),
			NodeName:   nullable(instance.NodeName),
			Phase:      instance.Phase,
			Stage:      nullable(instance.Stage),
			CreatedAt:  instant(instance.CreatedAt),
			LastError:  nullable(instance.LastError),
		})
	}
	return entries
}

func teardownGraceSeconds(grace *metav1.Duration) *int64 {
	if grace == nil {
		return nil
	}
	return ptr(seconds(grace.Duration))
}

func workloadNamespace(ref *v1alpha1.WorkloadRef) *string {
	if ref == nil {
		return nil
	}
	return nullable(ref.Namespace)
}

// a name the apiserver could never carry is refused here, since client-go rejects it locally and the failure is the request's rather than the cluster's
func refusedAsAnInvalidName(w http.ResponseWriter, name string) bool {
	violations := apivalidation.NameIsDNSSubdomain(name, false)
	if len(violations) == 0 {
		return false
	}
	writeAPIError(w, http.StatusBadRequest,
		fmt.Sprintf("%q cannot name a capacity lease: %s", name, strings.Join(violations, "; ")))
	return true
}

func writeLeaseNotFound(w http.ResponseWriter, name string) {
	writeAPIError(w, http.StatusNotFound,
		fmt.Sprintf("no capacity lease named %q exists in the cluster", name))
}

func (s *Server) leaseList(w http.ResponseWriter, r *http.Request) {
	var leases v1alpha1.CapacityLeaseList
	if err := s.client.List(r.Context(), &leases); err != nil {
		slog.Error("list the capacity leases", "error", err)
		writeAPIError(w, http.StatusBadGateway, leaseReadFailed)
		return
	}
	writeJSON(w, http.StatusOK, newLeaseListResponse(leases.Items, time.Now()))
}

func (s *Server) leaseDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if refusedAsAnInvalidName(w, name) {
		return
	}

	var lease v1alpha1.CapacityLease
	if err := s.client.Get(r.Context(), client.ObjectKey{Name: name}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			writeLeaseNotFound(w, name)
			return
		}
		slog.Error("read the capacity lease", "lease", name, "error", err)
		writeAPIError(w, http.StatusBadGateway, leaseReadFailed)
		return
	}
	writeJSON(w, http.StatusOK, newLeaseDetailResponse(&lease, time.Now()))
}
