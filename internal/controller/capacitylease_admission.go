package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

type sizingRejection struct {
	reason metrics.Reason
	cause  error
}

type sizing struct {
	instanceType string
	decision     *selectionDecision
}

func (r *CapacityLeaseReconciler) admit(ctx context.Context, lease *v1alpha1.CapacityLease, cfg *v1alpha1.ProviderConfig, prov provider.Provider) (ctrl.Result, error) {
	attributed := leaseAttribution{providerConfig: cfg.Name, region: unknownLabel}

	if err := requireOfferedRegion(prov, lease.Spec.Region); err != nil {
		return r.rejectLease(ctx, lease, attributed, reasonUnknownRegion, err)
	}
	attributed.region = lease.Spec.Region

	sized, rejected := r.resolveInstanceType(lease, cfg.Name)
	if rejected != nil {
		return r.rejectLease(ctx, lease, attributed, sizingCondition(lease), rejected.cause,
			selectionFailedRecord(attributed, selectionOf(lease), rejected.reason))
	}
	if sized.instanceType == "" {
		ctrl.LoggerFrom(ctx).Info("admitting a lease without validating its instance type",
			"providerConfig", cfg.Name, "region", lease.Spec.Region, "size", lease.Spec.Size)
	}
	attributed.instanceType = sized.instanceType

	if err := requireTeardownGuarantee(cfg, prov); err != nil {
		return r.rejectLease(ctx, lease, attributed, reasonProviderUnavailable, err)
	}
	return r.acceptLease(ctx, lease, attributed, r.latchSelection(ctx, lease, sized.decision))
}

func requireOfferedRegion(prov provider.Provider, region string) error {
	offered := prov.Capabilities().Regions
	if slices.Contains(offered, region) {
		return nil
	}
	return fmt.Errorf("region %q is not one of the regions the provider offers: %v", region, offered)
}

func sizingCondition(lease *v1alpha1.CapacityLease) string {
	if lease.Spec.Requirements != nil {
		return reasonUnsatisfiedRequirements
	}
	return reasonUnknownInstanceType
}

// a provider config whose listing keeps failing never fills the cache, so a pinned type stays unconfirmed rather than unusable
func (r *CapacityLeaseReconciler) resolveInstanceType(lease *v1alpha1.CapacityLease, config string) (sizing, *sizingRejection) {
	required := lease.Spec.Requirements
	region := lease.Spec.Region
	offered, err := r.Catalogue.List(config, region)

	switch {
	case errors.Is(err, catalogue.ErrUnavailable) && required == nil:
		return sizing{}, nil
	case err != nil:
		return sizing{}, &sizingRejection{reason: metrics.ReasonCatalogueUnavailable, cause: err}
	case len(offered) == 0:
		return sizing{}, &sizingRejection{
			reason: metrics.ReasonRegionUnavailable,
			cause:  fmt.Errorf("the catalogue offers no instance type in region %q", region),
		}
	case required != nil:
		return chooseInstanceType(offered, region, *required)
	case !offersType(offered, lease.Spec.Size):
		return sizing{}, &sizingRejection{
			reason: metrics.ReasonNoMatch,
			cause:  fmt.Errorf("instance type %q is not offered in region %q", lease.Spec.Size, region),
		}
	default:
		return sizing{instanceType: lease.Spec.Size}, nil
	}
}

func offersType(offered []provider.InstanceType, size string) bool {
	return slices.ContainsFunc(offered, func(it provider.InstanceType) bool { return it.Name == size })
}

func chooseInstanceType(offered []provider.InstanceType, region string, required v1alpha1.SizeRequirements) (sizing, *sizingRejection) {
	decision := selectInstanceType(offered, required)
	if !decision.qualified() {
		return sizing{}, &sizingRejection{
			reason: metrics.ReasonNoMatch,
			cause:  unsatisfiedRequirements(region, len(offered), decision.Rejected),
		}
	}
	return sizing{instanceType: decision.Chosen.Name, decision: &decision}, nil
}

func unsatisfiedRequirements(region string, offered int, tally map[rejectionReason]int) error {
	rejected := make([]string, 0, len(tally))
	for _, entry := range rejectedCounts(tally) {
		rejected = append(rejected, fmt.Sprintf("%s %d", entry.Reason, entry.Count))
	}
	return fmt.Errorf("no instance type in region %q qualified: %d offered, rejected by %s",
		region, offered, strings.Join(rejected, ", "))
}

func (r *CapacityLeaseReconciler) latchInstanceType(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Status.InstanceType != "" {
		return ctrl.Result{}, nil
	}
	sized, rejected := r.resolveInstanceType(lease, lease.Status.ProviderConfig)
	if rejected != nil || sized.instanceType == "" {
		return ctrl.Result{}, nil
	}

	lease.Status.InstanceType = sized.instanceType

	var records metricWrites
	records.add(selectedRecord(attributionOf(lease), selectionOf(lease)))
	records.add(r.latchSelection(ctx, lease, sized.decision))
	if err := r.writeStatus(ctx, lease, records...); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}

func (r *CapacityLeaseReconciler) latchSelection(ctx context.Context, lease *v1alpha1.CapacityLease, decision *selectionDecision) func() {
	if decision == nil || lease.Status.Selection != nil {
		return nil
	}
	lease.Status.Selection = selectionStatus(*decision, r.now())
	recorded := *lease.Status.Selection
	return func() { r.announceSelection(ctx, lease, *decision, recorded) }
}

func (r *CapacityLeaseReconciler) announceSelection(ctx context.Context, lease *v1alpha1.CapacityLease, decision selectionDecision, recorded v1alpha1.SelectionStatus) {
	ctrl.LoggerFrom(ctx).Info("selected an instance type from requirements",
		"instanceType", recorded.Chosen, "strategy", recorded.Strategy,
		"runnerUp", recorded.RunnerUp, "margin", decision.margin(),
		"considered", recorded.Considered, "rejected", recorded.Rejected)
	r.Recorder.Eventf(lease, nil, corev1.EventTypeNormal, reasonInstanceTypeSelected, actionSelectedInstanceType,
		"%s chose %s from %d qualifying candidates", recorded.Strategy, recorded.Chosen, recorded.Considered)
}
