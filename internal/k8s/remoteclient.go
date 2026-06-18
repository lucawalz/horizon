package k8s

import (
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type RemoteClientCache struct {
	mu      sync.Mutex
	clients map[string]cachedRemote
}

type cachedRemote struct {
	resourceVersion string
	client          kubernetes.Interface
	metrics         metricsclient.Interface
}

func NewRemoteClientCache() *RemoteClientCache {
	return &RemoteClientCache{clients: make(map[string]cachedRemote)}
}

func (c *RemoteClientCache) ClientsForKubeconfig(key, resourceVersion string, kubeconfig []byte) (kubernetes.Interface, metricsclient.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.clients[key]; ok && cached.resourceVersion == resourceVersion {
		return cached.client, cached.metrics, nil
	}

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("remote kubeconfig: %w", err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	applyRateLimits(restCfg)
	WrapAPITrace(restCfg)

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("remote clientset: %w", err)
	}

	metrics, err := metricsclient.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("remote metrics clientset: %w", err)
	}

	c.clients[key] = cachedRemote{resourceVersion: resourceVersion, client: clientset, metrics: metrics}
	return clientset, metrics, nil
}
