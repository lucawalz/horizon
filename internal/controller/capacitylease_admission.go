package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
)

func (r *CapacityLeaseReconciler) admit(ctx context.Context, lease *v1alpha1.CapacityLease, cfg *v1alpha1.ProviderConfig, prov provider.Provider) (ctrl.Result, error) {
	attributed := leaseAttribution{providerConfig: cfg.Name, region: unknownLabel}

	if err := requireOfferedRegion(prov, lease.Spec.Region); err != nil {
		return r.rejectLease(ctx, lease, attributed, reasonUnknownRegion, err)
	}
	attributed.region = lease.Spec.Region

	catalogued, err := r.catalogueOffers(cfg.Name, lease.Spec.Region, lease.Spec.Size)
	if err != nil {
		return r.rejectLease(ctx, lease, attributed, reasonUnknownInstanceType, err)
	}
	if catalogued {
		attributed.instanceType = lease.Spec.Size
	} else {
		ctrl.LoggerFrom(ctx).Info("admitting a lease without validating its instance type",
			"providerConfig", cfg.Name, "region", lease.Spec.Region, "size", lease.Spec.Size)
	}

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

// a provider config whose listing keeps failing never fills the cache, so an unconfirmed size must not be latched
func (r *CapacityLeaseReconciler) catalogueOffers(config, region, size string) (bool, error) {
	offered, err := r.Catalogue.List(config, region)
	switch {
	case errors.Is(err, catalogue.ErrUnavailable):
		return false, nil
	case err != nil:
		return false, err
	}

	for _, it := range offered {
		if it.Name == size {
			return true, nil
		}
	}
	return false, fmt.Errorf("instance type %q is not offered in region %q", size, region)
}

func (r *CapacityLeaseReconciler) latchInstanceType(ctx context.Context, lease *v1alpha1.CapacityLease) (ctrl.Result, error) {
	if lease.Status.InstanceType != "" {
		return ctrl.Result{}, nil
	}
	catalogued, err := r.catalogueOffers(lease.Status.ProviderConfig, lease.Status.Region, lease.Spec.Size)
	if err != nil || !catalogued {
		return ctrl.Result{}, nil
	}

	lease.Status.InstanceType = lease.Spec.Size
	if err := r.writeStatus(ctx, lease); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: stepRequeue}, nil
}
