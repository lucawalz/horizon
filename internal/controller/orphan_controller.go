package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const orphanControllerName = "orphan"

const (
	orphanSweepInterval = time.Minute
	orphanExpiryGrace   = 5 * time.Minute
)

type OrphanReconciler struct {
	Client   client.Client
	Provider provider.Provider
	Clock    func() time.Time
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

	stranded, err := r.nodeIsStranded(ctx, &node, leaseUID)
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

func (r *OrphanReconciler) nodeIsStranded(ctx context.Context, node *corev1.Node, leaseUID string) (bool, error) {
	if nodeReportsReady(node) {
		return false, nil
	}

	live, err := r.liveLeaseUIDs(ctx)
	if err != nil {
		return false, err
	}
	if live[leaseUID] {
		return false, nil
	}

	return r.instanceIsAbsent(ctx, node.Name)
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
	instances, err := r.Provider.List(ctx, map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue})
	if err != nil {
		return fmt.Errorf("orphan: list provider instances: %w", err)
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
		ctrl.LoggerFrom(ctx).Info("deleting expired instance", "instance", inst.Name)
		if err := r.destroy(ctx, inst.Name); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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

func (r *OrphanReconciler) destroy(ctx context.Context, name string) error {
	if err := r.Provider.Delete(ctx, name); err != nil {
		return fmt.Errorf("orphan: delete instance %q: %w", name, err)
	}

	absent, err := r.instanceIsAbsent(ctx, name)
	if err != nil {
		return err
	}
	if !absent {
		return fmt.Errorf("orphan: instance %q still present after delete", name)
	}
	return nil
}

func (r *OrphanReconciler) instanceIsAbsent(ctx context.Context, name string) (bool, error) {
	_, err := r.Provider.Get(ctx, name)
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("orphan: get instance %q: %w", name, err)
	default:
		return false, nil
	}
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
		return errors.New("orphan: provider is required")
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Named(orphanControllerName).
		Complete(r); err != nil {
		return err
	}
	return mgr.Add(r)
}

func nodeReportsReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
