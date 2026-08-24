package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	unreadableRequest   = "the request body is not a lease this interface can submit"
	unreadableExtension = "the request body is not an extension this interface can submit"
	leaseCreateFailed   = "the capacity lease could not be created in the cluster"
	leaseReleaseFailed  = "the capacity lease could not be released"
	leaseExtendFailed   = "the capacity lease could not be extended"
	releaseRequested    = "the controller was asked to release this lease. it drains the leased nodes and " +
		"deletes their machines, and the watchdog on each node powers it off on a clock of its own regardless"
	extensionRequested = "the controller re-derives the deadline of this lease on its next pass, and each leased " +
		"node follows it once its watchdog renews. a deadline past the lifetime backstop of a machine is held at that backstop"
	extensionOnly = "this interface lengthens a lease rather than shortening one. a duration shorter than the one a " +
		"lease already carries can put its deadline in the past, which leaves no teardown budget and deletes the leased " +
		"nodes without draining them. releasing the lease gives the capacity back with a drain"
)

type leaseRequirementsRequest struct {
	MinCPU       int32  `json:"minCPU"`
	MinMemory    string `json:"minMemory"`
	Architecture string `json:"architecture"`
	CPUType      string `json:"cpuType"`
	Strategy     string `json:"strategy"`
}

type leaseCreateRequest struct {
	Name                 string                    `json:"name"`
	ProviderRef          string                    `json:"providerRef"`
	Region               string                    `json:"region"`
	Size                 string                    `json:"size"`
	Requirements         *leaseRequirementsRequest `json:"requirements"`
	Replicas             int32                     `json:"replicas"`
	DurationSeconds      int64                     `json:"durationSeconds"`
	TeardownGraceSeconds *int64                    `json:"teardownGraceSeconds"`
	WorkloadNamespace    string                    `json:"workloadNamespace"`
}

type leaseExtendRequest struct {
	DurationSeconds int64 `json:"durationSeconds"`
}

type leaseReleaseResponse struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

type leaseExtendResponse struct {
	Name            string `json:"name"`
	DurationSeconds int64  `json:"durationSeconds"`
	Detail          string `json:"detail"`
}

func (r *leaseRequirementsRequest) sizeRequirements() (*v1alpha1.SizeRequirements, error) {
	if r == nil {
		return nil, nil
	}

	requirements := &v1alpha1.SizeRequirements{
		MinCPU:       r.MinCPU,
		Architecture: v1alpha1.Architecture(r.Architecture),
		CPUType:      v1alpha1.CPUType(r.CPUType),
		Strategy:     v1alpha1.SizingStrategy(r.Strategy),
	}
	if r.MinMemory == "" {
		return requirements, nil
	}

	quantity, err := resource.ParseQuantity(r.MinMemory)
	if err != nil {
		return nil, fmt.Errorf("the minimum memory %q is not a quantity: %w", r.MinMemory, err)
	}
	requirements.MinMemory = &quantity
	return requirements, nil
}

func (r leaseCreateRequest) lease() (*v1alpha1.CapacityLease, error) {
	requirements, err := r.Requirements.sizeRequirements()
	if err != nil {
		return nil, err
	}
	return &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: r.Name},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef:   r.ProviderRef,
			Region:        r.Region,
			Size:          r.Size,
			Requirements:  requirements,
			Replicas:      r.Replicas,
			Duration:      metav1.Duration{Duration: span(r.DurationSeconds)},
			TeardownGrace: teardownGrace(r.TeardownGraceSeconds),
			Workload:      workloadRefFor(r.WorkloadNamespace),
		},
	}, nil
}

func teardownGrace(elapsed *int64) *metav1.Duration {
	if elapsed == nil {
		return nil
	}
	return &metav1.Duration{Duration: span(*elapsed)}
}

func workloadRefFor(namespace string) *v1alpha1.WorkloadRef {
	if namespace == "" {
		return nil
	}
	return &v1alpha1.WorkloadRef{Namespace: namespace}
}

// the apiserver names the rule a rejected lease broke, which is more accurate than anything this handler can restate
func writeClusterRefusal(w http.ResponseWriter, r *http.Request, err error, name, fallback string) {
	if apierrors.IsNotFound(err) {
		writeLeaseNotFound(w, name)
		return
	}
	if refusedByAuthorisation(w, r, err) {
		return
	}

	var refused apierrors.APIStatus
	if errors.As(err, &refused) && refused.Status().Code >= http.StatusBadRequest && refused.Status().Message != "" {
		writeAPIError(w, int(refused.Status().Code), refused.Status().Message)
		return
	}

	slog.Error("mutate the capacity lease", "lease", name, "error", err)
	writeAPIError(w, http.StatusBadGateway, fallback)
}

func (s *Server) leaseCreate(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers)
	if !held {
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var submitted leaseCreateRequest
	if err := decoder.Decode(&submitted); err != nil {
		writeAPIError(w, http.StatusBadRequest, unreadableRequest+": "+err.Error())
		return
	}

	lease, err := submitted.lease()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := writer.Create(r.Context(), lease); err != nil {
		writeClusterRefusal(w, r, err, lease.Name, leaseCreateFailed)
		return
	}
	writeJSON(w, http.StatusCreated, newLeaseDetailResponse(lease, time.Now()))
}

// deleting the lease asks the controller for a teardown through its finalizer rather than destroying anything here
func (s *Server) leaseRelease(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers)
	if !held {
		return
	}

	name := r.PathValue("name")
	if refusedAsAnInvalidName(w, name) {
		return
	}

	lease := &v1alpha1.CapacityLease{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := writer.Delete(r.Context(), lease); err != nil {
		writeClusterRefusal(w, r, err, name, leaseReleaseFailed)
		return
	}
	writeJSON(w, http.StatusAccepted, leaseReleaseResponse{Name: name, Detail: releaseRequested})
}

func (s *Server) leaseExtend(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers)
	if !held {
		return
	}

	name := r.PathValue("name")
	if refusedAsAnInvalidName(w, name) {
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var submitted leaseExtendRequest
	if err := decoder.Decode(&submitted); err != nil {
		writeAPIError(w, http.StatusBadRequest, unreadableExtension+": "+err.Error())
		return
	}

	// only a lengthening is accepted, so two of these racing can still only lengthen and the read needs no lock
	lease, read := s.leaseNamed(w, r, name)
	if !read {
		return
	}

	requested := span(submitted.DurationSeconds)
	if requested <= lease.Spec.Duration.Duration {
		writeAPIError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("%q already runs for %s. %s", name, lease.Spec.Duration.Duration, extensionOnly))
		return
	}

	if err := writer.Extend(r.Context(), name, requested); err != nil {
		writeClusterRefusal(w, r, err, name, leaseExtendFailed)
		return
	}
	writeJSON(w, http.StatusAccepted, leaseExtendResponse{
		Name:            name,
		DurationSeconds: seconds(requested),
		Detail:          extensionRequested,
	})
}
