package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type ReservedProviderFunc func(ctx context.Context, kc kubernetes.Interface, cfg config.Reserved) (provider.Provider, error)

type App struct {
	Config        *config.Config
	KubeClient    kubernetes.Interface
	MetricsClient metricsclient.Interface
	FluxClient    *k8s.FluxClient
	Cluster       string
	Context       string
	NewReserved   ReservedProviderFunc
}

func NewApp(contextName, clusterName string, reserved ReservedProviderFunc) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	effectiveContext := contextName
	if effectiveContext == "" {
		effectiveContext = cfg.Context
	}

	kc, err := k8s.NewClientForContext(cfg.Kubeconfig, effectiveContext)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	mc, err := k8s.NewMetricsClient(cfg.Kubeconfig, effectiveContext)
	if err != nil {
		return nil, fmt.Errorf("metrics client: %w", err)
	}

	fc, err := k8s.NewFluxClient(cfg.Kubeconfig, effectiveContext)
	if err != nil {
		return nil, fmt.Errorf("flux client: %w", err)
	}

	cluster := clusterName
	if cluster == "" {
		cluster = cfg.Cluster
	}

	return &App{
		Config:        cfg,
		KubeClient:    kc,
		MetricsClient: mc,
		FluxClient:    fc,
		Cluster:       cluster,
		Context:       effectiveContext,
		NewReserved:   reserved,
	}, nil
}

func (a *App) ReservedProvider(ctx context.Context) (provider.Provider, error) {
	if a.NewReserved == nil {
		return nil, fmt.Errorf("core: reserved provider not configured")
	}
	return a.NewReserved(ctx, a.KubeClient, a.Config.Reserved)
}
