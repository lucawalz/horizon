package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	if sized == "" {
		ctrl.LoggerFrom(ctx).Info("admitting a lease without validating its instance type",
			"providerConfig", cfg.Name, "region", lease.Spec.Region, "size", lease.Spec.Size)
	}
	attributed.instanceType = sized

	if err := requireTeardownGuarantee(cfg, prov); err != nil {
		return r.rejectLease(ctx, lease, attributed, reasonProviderUnavailable, err)
	}
	return r.acceptLease(ctx, lease, attributed)
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
func (r *CapacityLeaseReconciler) resolveInstanceType(lease *v1alpha1.CapacityLease, config string) (string, *sizingRejection) {
	required := lease.Spec.Requirements
	region := lease.Spec.Region
	offered, err := r.Catalogue.List(config, region)

	switch {
	case errors.Is(err, catalogue.ErrUnavailable) && required == nil:
		return "", nil
	case err != nil:
		return "", &sizingRejection{reason: metrics.ReasonCatalogueUnavailable, cause: err}
	case len(offered) == 0:
		return "", &sizingRejection{
			reason: metrics.ReasonRegionUnavailable,
			cause:  fmt.Errorf("the catalogue offers no instance type in region %q", region),
		}
	case required != nil:
		return chooseInstanceType(offered, region, *required)
	case !offersType(offered, lease.Spec.Size):
		return "", &sizingRejection{
			reason: metrics.ReasonNoMatch,
			cause:  fmt.Errorf("instance type %q is not offered in region %q", lease.Spec.Size, region),
		}
	default:
		return lease.Spec.Size, nil
	}
}

func offersType(offered []provider.InstanceType, size string) bool {
	return slices.ContainsFunc(offered, func(it provider.InstanceType) bool { return it.Name == size })
}

func chooseInstanceType(offered []provider.InstanceType, region string, required v1alpha1.SizeRequirements) (string, *sizingRejection) {
	chosen, found := selectInstanceType(offered, required)
	if !found {
		return "", &sizingRejection{
			reason: metrics.ReasonNoMatch,
			cause:  fmt.Errorf("no instance type in region %q offers %d cores on %s", region, required.MinCPU, required.Architecture),
		}
	}
	return chosen.Name, nil
}

func (r *CapacityLeaseReconciler) latchInstanceType(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Status.InstanceType != "" {
		return ctrl.Result{}, nil
	}
	sized, rejected := r.resolveInstanceType(lease, lease.Status.ProviderConfig)
	if rejected != nil || sized == "" {
		return ctrl.Result{}, nil
	}

	lease.Status.InstanceType = sized
	if err := r.writeStatus(ctx, lease, selectedRecord(attributionOf(lease), selectionOf(lease))); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}
