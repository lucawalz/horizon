package k8s

import (
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type RemoteClientCache struct {
	mu      sync.Mutex
	clients map[string]cachedRemote
}

type cachedRemote struct {
	resourceVersion string
	client          kubernetes.Interface
}

func NewRemoteClientCache() *RemoteClientCache {
	return &RemoteClientCache{clients: make(map[string]cachedRemote)}
}

func (c *RemoteClientCache) ClientForKubeconfig(key, resourceVersion string, kubeconfig []byte) (kubernetes.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.clients[key]; ok && cached.resourceVersion == resourceVersion {
		return cached.client, nil
	}

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("remote kubeconfig: %w", err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	applyRateLimits(restCfg)
	WrapAPITrace(restCfg)

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("remote clientset: %w", err)
	}

	c.clients[key] = cachedRemote{resourceVersion: resourceVersion, client: clientset}
	return clientset, nil
}
