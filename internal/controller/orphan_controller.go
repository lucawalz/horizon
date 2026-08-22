package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

const orphanControllerName = "orphan"

const (
	orphanSweepInterval = time.Minute
	orphanExpiryGrace   = 5 * time.Minute
)

type OrphanReconciler struct {
	Client   client.Client
	Provider ProviderFactory
	Clock    func() time.Time
}

type configuredProvider struct {
	provider.Provider
	config string
}

func (r *OrphanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Client.Get(ctx, req.NamespacedName, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	leaseUID, owned := node.Labels[LeaseUIDLabelKey]
	if !owned {
		return ctrl.Result{}, nil
	}

	// a node that strands later must first lose readiness, and that transition wakes the watch
	if nodeReady(&node) {
		return ctrl.Result{}, nil
	}

	stranded, err := r.leaseAndInstanceAreGone(ctx, &node, leaseUID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !stranded {
		return ctrl.Result{RequeueAfter: orphanSweepInterval}, nil
	}

	ctrl.LoggerFrom(ctx).Info("deleting stranded node", "node", node.Name, "leaseUID", leaseUID)
	if err := r.Client.Delete(ctx, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *OrphanReconciler) leaseAndInstanceAreGone(ctx context.Context, node *corev1.Node, leaseUID string) (bool, error) {
	live, err := r.liveLeaseUIDs(ctx)
	if err != nil {
		return false, err
	}
	if live[leaseUID] {
		return false, nil
	}

	return r.instanceIsAbsentEverywhere(ctx, node.Name)
}

func (r *OrphanReconciler) instanceIsAbsentEverywhere(ctx context.Context, name string) (bool, error) {
	provs, err := r.providers(ctx)
	if err != nil {
		return false, err
	}
	if len(provs) == 0 {
		return false, nil
	}

	for _, prov := range provs {
		absent, err := instanceIsAbsent(ctx, prov, name)
		if err != nil || !absent {
			return false, err
		}
	}
	return true, nil
}

func (r *OrphanReconciler) Start(ctx context.Context) error {
	ticker := time.NewTicker(orphanSweepInterval)
	defer ticker.Stop()

	for {
		if err := r.sweep(ctx); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "sweep expired provider instances")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *OrphanReconciler) NeedLeaderElection() bool {
	return true
}

func (r *OrphanReconciler) sweep(ctx context.Context) error {
	provs, buildErr := r.providers(ctx)

	failures := []error{buildErr}
	for _, prov := range provs {
		failures = append(failures, r.sweepProvider(ctx, prov))
	}
	return errors.Join(failures...)
}

func (r *OrphanReconciler) sweepProvider(ctx context.Context, prov configuredProvider) error {
	instances, err := prov.List(ctx, map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue})
	if err != nil {
		return fmt.Errorf("orphan: list instances of %q: %w", prov.config, err)
	}

	live, err := r.liveLeaseUIDs(ctx)
	if err != nil {
		return err
	}

	var failures []error
	for _, inst := range instances {
		if !r.instanceIsExpired(inst, live) {
			continue
		}
		ctrl.LoggerFrom(ctx).Info("deleting expired instance", "instance", inst.Name, "providerConfig", prov.config)
		if err := destroyInstance(ctx, prov, inst.Name); err != nil {
			failures = append(failures, err)
			continue
		}
		metrics.RecordInstanceReleased(prov.config, inst.Region, inst.Size, metrics.PathOrphan, inst.CreatedAt, r.now())
		metrics.RecordOrphanInstanceDeleted(prov.config, inst.Region)
	}
	return errors.Join(failures...)
}

func (r *OrphanReconciler) providers(ctx context.Context) ([]configuredProvider, error) {
	if r.Provider == nil {
		return nil, errors.New("orphan: no provider factory configured")
	}

	var configs v1alpha1.ProviderConfigList
	if err := r.Client.List(ctx, &configs); err != nil {
		return nil, fmt.Errorf("orphan: list provider configs: %w", err)
	}

	provs := make([]configuredProvider, 0, len(configs.Items))
	var failures []error
	for i := range configs.Items {
		cfg := &configs.Items[i]
		built, err := r.Provider(ctx, cfg)
		if err != nil {
			failures = append(failures, fmt.Errorf("orphan: build provider from %q: %w", cfg.Name, err))
			continue
		}
		provs = append(provs, configuredProvider{Provider: built, config: cfg.Name})
	}
	return provs, errors.Join(failures...)
}

func (r *OrphanReconciler) instanceIsExpired(inst provider.Instance, live map[string]bool) bool {
	uid, owned := inst.Labels[LeaseUIDLabelKey]
	if !owned || live[uid] {
		return false
	}

	deadline, ok := provider.ParseExpiry(inst.Labels)
	if !ok {
		return true
	}
	return !r.now().Before(deadline.Add(orphanExpiryGrace))
}

func destroyInstance(ctx context.Context, prov configuredProvider, name string) error {
	if err := prov.Delete(ctx, name); err != nil {
		return fmt.Errorf("orphan: delete instance %q of %q: %w", name, prov.config, err)
	}

	absent, err := instanceIsAbsent(ctx, prov, name)
	if err != nil {
		return err
	}
	if !absent {
		return fmt.Errorf("orphan: instance %q of %q still present after delete", name, prov.config)
	}
	return nil
}

func instanceIsAbsent(ctx context.Context, prov configuredProvider, name string) (bool, error) {
	absent, err := provider.ConfirmAbsent(ctx, prov, name)
	if err != nil {
		return false, fmt.Errorf("orphan: get instance %q of %q: %w", name, prov.config, err)
	}
	return absent, nil
}

func (r *OrphanReconciler) liveLeaseUIDs(ctx context.Context) (map[string]bool, error) {
	var leases v1alpha1.CapacityLeaseList
	if err := r.Client.List(ctx, &leases); err != nil {
		return nil, fmt.Errorf("orphan: list capacity leases: %w", err)
	}

	uids := make(map[string]bool, len(leases.Items))
	for i := range leases.Items {
		uids[string(leases.Items[i].UID)] = true
	}
	return uids, nil
}

func (r *OrphanReconciler) now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock()
}

func (r *OrphanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Provider == nil {
		return errors.New("orphan: provider factory is required")
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(orphanNodeSignals())).
		Named(orphanControllerName).
		Complete(r); err != nil {
		return err
	}
	return mgr.Add(r)
}

func orphanNodeSignals() predicate.Predicate {
	return nodeSignals(LeaseUIDLabelKey)
}
