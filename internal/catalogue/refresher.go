package catalogue

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const refresherName = "catalogue"

const RefreshInterval = time.Hour

type Lister interface {
	Capabilities() provider.Capabilities
	ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error)
}

type ListerFactory func(ctx context.Context, cfg *v1alpha1.ProviderConfig) (Lister, error)

type RefreshCounts struct {
	Success uint64
	Failure uint64
}

type Refresher struct {
	Client   client.Client
	Lister   ListerFactory
	Cache    *Cache
	Interval time.Duration

	successes atomic.Uint64
	failures  atomic.Uint64
}

func (r *Refresher) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.interval())
	defer ticker.Stop()

	for {
		if err := r.refreshAll(ctx); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "refresh the instance type catalogue")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Refresher) NeedLeaderElection() bool {
	return false
}

func (r *Refresher) Counts() RefreshCounts {
	return RefreshCounts{Success: r.successes.Load(), Failure: r.failures.Load()}
}

func (r *Refresher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cfg v1alpha1.ProviderConfig
	if err := r.Client.Get(ctx, req.NamespacedName, &cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, r.refresh(ctx, &cfg)
}

func (r *Refresher) refreshAll(ctx context.Context) error {
	var configs v1alpha1.ProviderConfigList
	if err := r.Client.List(ctx, &configs); err != nil {
		return fmt.Errorf("catalogue: list provider configs: %w", err)
	}

	live := make([]string, 0, len(configs.Items))
	var failures []error
	for i := range configs.Items {
		cfg := &configs.Items[i]
		live = append(live, cfg.Name)
		failures = append(failures, r.refresh(ctx, cfg))
	}
	r.Cache.retain(live)
	return errors.Join(failures...)
}

func (r *Refresher) refresh(ctx context.Context, cfg *v1alpha1.ProviderConfig) error {
	types, err := r.fetch(ctx, cfg)
	if err != nil {
		r.failures.Add(1)
		return fmt.Errorf("catalogue: refresh provider config %q: %w", cfg.Name, err)
	}
	r.successes.Add(1)
	r.Cache.store(cfg.Name, types)
	return nil
}

func (r *Refresher) fetch(ctx context.Context, cfg *v1alpha1.ProviderConfig) ([]provider.InstanceType, error) {
	if r.Lister == nil {
		return nil, errors.New("no lister factory is configured")
	}
	lister, err := r.Lister(ctx, cfg)
	if err != nil {
		return nil, err
	}

	var types []provider.InstanceType
	for _, region := range lister.Capabilities().Regions {
		offered, err := lister.ListInstanceTypes(ctx, region)
		if err != nil {
			return nil, err
		}
		types = append(types, offered...)
	}
	return types, nil
}

func (r *Refresher) interval() time.Duration {
	if r.Interval <= 0 {
		return RefreshInterval
	}
	return r.Interval
}

func (r *Refresher) SetupWithManager(mgr ctrl.Manager) error {
	if r.Lister == nil {
		return errors.New("catalogue: lister factory is required")
	}
	if r.Cache == nil {
		return errors.New("catalogue: cache is required")
	}

	servedByEveryReplica := false
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ProviderConfig{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named(refresherName).
		WithOptions(controller.Options{NeedLeaderElection: &servedByEveryReplica}).
		Complete(r); err != nil {
		return err
	}
	return mgr.Add(r)
}
