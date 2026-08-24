// Package manager wires the in-cluster controller runtime for horizon.
package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/controller"
	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/provider"
)

const LeaderElectionID = "horizon-operator.horizon.dev"

const DefaultPollInterval = controller.DefaultPollInterval

type Options struct {
	MetricsAddress string
	HealthAddress  string
	LeaderElection bool
	PollInterval   time.Duration
}

func Scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func uncachedTypes() []client.Object {
	return []client.Object{
		&corev1.Pod{},
		&corev1.Secret{},
		&appsv1.Deployment{},
		&appsv1.StatefulSet{},
	}
}

func cacheOptions() cache.Options {
	poolNodes, err := labels.Parse(provider.PoolLabelKey)
	utilruntime.Must(err)
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Node{}: {Label: poolNodes},
		},
	}
}

func New(restConfig *rest.Config, opts Options) (ctrl.Manager, error) {
	mgr, _, err := newManager(restConfig, opts)
	return mgr, err
}

func newManager(restConfig *rest.Config, opts Options) (ctrl.Manager, *reconcilers, error) {
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:  Scheme(),
		Cache:   cacheOptions(),
		Metrics: metricsserver.Options{BindAddress: opts.MetricsAddress},
		Client: client.Options{
			Cache: &client.CacheOptions{DisableFor: uncachedTypes()},
		},
		HealthProbeBindAddress:        opts.HealthAddress,
		LeaderElection:                opts.LeaderElection,
		LeaderElectionID:              LeaderElectionID,
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return nil, nil, fmt.Errorf("add liveness check: %w", err)
	}
	if err := mgr.AddReadyzCheck("cache-sync", cacheSyncChecker(mgr)); err != nil {
		return nil, nil, fmt.Errorf("add readiness check: %w", err)
	}

	if err := metrics.SetLeaseStateSource(leaseStateSource(mgr.GetCache(), leaseStateReadTimeout)); err != nil {
		return nil, nil, fmt.Errorf("serve the lease state gauges: %w", err)
	}

	kube, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}

	parts, err := newReconcilers(mgr.GetClient(), kube,
		mgr.GetEventRecorder(controller.CapacityLeaseControllerName), opts.PollInterval)
	if err != nil {
		return nil, nil, err
	}
	if err := parts.setupWithManager(mgr); err != nil {
		return nil, nil, err
	}

	return mgr, parts, nil
}

type reconcilers struct {
	leases    *controller.CapacityLeaseReconciler
	orphans   *controller.OrphanReconciler
	refresher *catalogue.Refresher
}

// the refresher fills the very cache the lease controller validates against, so both must hold the same one
func newReconcilers(api client.Client, kube kubernetes.Interface, recorder events.EventRecorder, pollInterval time.Duration) (*reconcilers, error) {
	providers, err := controller.NewProviderFactory(kube)
	if err != nil {
		return nil, err
	}
	listers, err := controller.NewCatalogueFactory(kube)
	if err != nil {
		return nil, err
	}
	publisher, err := controller.NewProviderConfigPublisher(api, kube)
	if err != nil {
		return nil, err
	}

	types := catalogue.NewCache()
	return &reconcilers{
		leases: &controller.CapacityLeaseReconciler{
			Client:       api,
			Kube:         kube,
			Provider:     providers,
			Catalogue:    types,
			Recorder:     recorder,
			PollInterval: pollInterval,
		},
		orphans: &controller.OrphanReconciler{
			Client:   api,
			Provider: providers,
		},
		refresher: &catalogue.Refresher{
			Client:    api,
			Lister:    listers,
			Cache:     types,
			Publisher: publisher,
		},
	}, nil
}

func (p *reconcilers) setupWithManager(mgr ctrl.Manager) error {
	if err := p.leases.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("set up capacity lease controller: %w", err)
	}
	if err := p.orphans.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("set up orphan controller: %w", err)
	}
	if err := p.refresher.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("set up catalogue refresher: %w", err)
	}
	return nil
}

func cacheSyncChecker(mgr ctrl.Manager) healthz.Checker {
	return func(req *http.Request) error {
		if !mgr.GetCache().WaitForCacheSync(req.Context()) {
			return errors.New("informer cache not synced")
		}
		return nil
	}
}

func Run(ctx context.Context, opts Options) error {
	ctrl.SetLogger(klog.Background())

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("resolve kubernetes config: %w", err)
	}

	mgr, err := New(restConfig, opts)
	if err != nil {
		return err
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}
