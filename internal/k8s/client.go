package k8s

import (
	"fmt"
	"os"
	"sort"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	clientQPS   = 50
	clientBurst = 100
)

func InCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func applyRateLimits(cfg *rest.Config) {
	cfg.QPS = clientQPS
	cfg.Burst = clientBurst
}

func RestConfig(kubeconfigPath string) (*rest.Config, error) {
	return RestConfigForContext(kubeconfigPath, "")
}

func RestConfigForContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	if kubeconfigPath == "" && contextName == "" && InCluster() {
		restCfg, err := rest.InClusterConfig()
		if err == nil {
			restCfg.WarningHandler = rest.NoWarnings{}
			applyRateLimits(restCfg)
			WrapAPITrace(restCfg)
			return restCfg, nil
		}
		if err != rest.ErrNotInCluster {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, overrides,
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig %q context %q: %w", kubeconfigPath, contextName, err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	applyRateLimits(restCfg)
	WrapAPITrace(restCfg)
	return restCfg, nil
}

func Contexts(kubeconfigPath string) (names []string, current string, err error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	apiCfg, err := rules.Load()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig %q: %w", kubeconfigPath, err)
	}
	names = make([]string, 0, len(apiCfg.Contexts))
	for name := range apiCfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, apiCfg.CurrentContext, nil
}

func NewClientForContext(kubeconfigPath, contextName string) (kubernetes.Interface, error) {
	restCfg, err := RestConfigForContext(kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}
	return clientset, nil
}

func NewMetricsClient(kubeconfigPath, contextName string) (metricsclient.Interface, error) {
	restCfg, err := RestConfigForContext(kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}
	clientset, err := metricsclient.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("metrics clientset: %w", err)
	}
	return clientset, nil
}
