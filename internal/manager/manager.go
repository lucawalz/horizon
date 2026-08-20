// Package manager wires the in-cluster controller runtime for horizon.
package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/controller"
	"github.com/lucawalz/horizon/internal/provider"
)

const LeaderElectionID = "horizon-operator.horizon.dev"

type Options struct {
	MetricsAddress string
	HealthAddress  string
	LeaderElection bool
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
		return nil, fmt.Errorf("build manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return nil, fmt.Errorf("add liveness check: %w", err)
	}
	if err := mgr.AddReadyzCheck("cache-sync", cacheSyncChecker(mgr)); err != nil {
		return nil, fmt.Errorf("add readiness check: %w", err)
	}

	kube, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}

	providers, err := controller.NewProviderFactory(kube)
	if err != nil {
		return nil, err
	}

	leases := &controller.CapacityLeaseReconciler{
		Client:   mgr.GetClient(),
		Kube:     kube,
		Provider: providers,
		Recorder: mgr.GetEventRecorder(controller.CapacityLeaseControllerName),
	}
	if err := leases.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("set up capacity lease controller: %w", err)
	}

	orphans := &controller.OrphanReconciler{
		Client:   mgr.GetClient(),
		Provider: providers,
	}
	if err := orphans.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("set up orphan controller: %w", err)
	}

	if err := addCatalogue(mgr, kube); err != nil {
		return nil, err
	}

	return mgr, nil
}

func addCatalogue(mgr ctrl.Manager, kube kubernetes.Interface) error {
	listers, err := controller.NewCatalogueFactory(kube)
	if err != nil {
		return err
	}

	refresher := &catalogue.Refresher{
		Client: mgr.GetClient(),
		Lister: listers,
		Cache:  catalogue.NewCache(),
	}
	if err := refresher.SetupWithManager(mgr); err != nil {
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
