package k8s

import (
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func InCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func RestConfig(kubeconfigPath string) (*rest.Config, error) {
	return RestConfigForContext(kubeconfigPath, "")
}

func RestConfigForContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	if kubeconfigPath == "" && contextName == "" && InCluster() {
		restCfg, err := rest.InClusterConfig()
		if err == nil {
			restCfg.WarningHandler = rest.NoWarnings{}
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
	return restCfg, nil
}

func NewClient(kubeconfigPath string) (kubernetes.Interface, error) {
	return NewClientForContext(kubeconfigPath, "")
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
