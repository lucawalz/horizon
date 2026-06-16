package core

import (
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/k8s"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type App struct {
	Config        *config.Config
	KubeClient    kubernetes.Interface
	MetricsClient metricsclient.Interface
	CapiClient    *capi.Client
	Cluster       string
	Context       string
}

func NewApp(contextName, clusterName string) (*App, error) {
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

	cc, err := capi.NewClientForContext(cfg.Kubeconfig, effectiveContext)
	if err != nil {
		return nil, fmt.Errorf("capi client: %w", err)
	}

	cluster := clusterName
	if cluster == "" {
		cluster = cfg.Cluster
	}

	return &App{
		Config:        cfg,
		KubeClient:    kc,
		MetricsClient: mc,
		CapiClient:    cc,
		Cluster:       cluster,
		Context:       effectiveContext,
	}, nil
}
