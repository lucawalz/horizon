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
	if err := requireOfferedRegion(prov, lease.Spec.Region); err != nil {
		return r.rejectLease(ctx, lease, unattributed, reasonUnknownRegion, err)
	}
	if err := r.requireCataloguedSize(ctx, lease, cfg.Name); err != nil {
		return r.rejectLease(ctx, lease, cfg.Name, reasonUnknownInstanceType, err)
	}
	if err := requireTeardownGuarantee(cfg, prov); err != nil {
		return r.rejectLease(ctx, lease, cfg.Name, reasonProviderUnavailable, err)
	}
	return r.acceptLease(ctx, lease)
}

func requireOfferedRegion(prov provider.Provider, region string) error {
	offered := prov.Capabilities().Regions
	if slices.Contains(offered, region) {
		return nil
	}
	return fmt.Errorf("region %q is not one of the regions the provider offers: %v", region, offered)
}

func (r *CapacityLeaseReconciler) requireCataloguedSize(ctx context.Context, lease *v1alpha1.CapacityLease, config string) error {
	offered, err := r.Catalogue.List(config, lease.Spec.Region)
	switch {
	case errors.Is(err, catalogue.ErrUnavailable):
		ctrl.LoggerFrom(ctx).Info("admitting a lease without validating its instance type",
			"providerConfig", config, "region", lease.Spec.Region, "size", lease.Spec.Size)
		return nil
	case err != nil:
		return err
	}

	for _, it := range offered {
		if it.Name == lease.Spec.Size {
			return nil
		}
	}
	return fmt.Errorf("instance type %q is not offered in region %q", lease.Spec.Size, lease.Spec.Region)
}
