package velero

import (
	"context"
	"fmt"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	crClient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupNamespace        = "velero"
	defaultStorageLocation = "default"
)

type Client struct {
	cl crClient.Client
}

func NewClient(kubeconfigPath string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("velero: kubeconfig %q: %w", kubeconfigPath, err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}

	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("velero: scheme: %w", err)
	}
	cl, err := crClient.New(restCfg, crClient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("velero: client: %w", err)
	}
	return &Client{cl: cl}, nil
}

func NewClientWithCRClient(cl crClient.Client) *Client {
	return &Client{cl: cl}
}

func (c *Client) TriggerBackup(ctx context.Context, workloadNamespace, name string, poll, timeout time.Duration) error {
	backup := &velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: backupNamespace,
		},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: []string{workloadNamespace},
			StorageLocation:    defaultStorageLocation,
		},
	}
	if err := c.cl.Create(ctx, backup); err != nil {
		return fmt.Errorf("velero: backup create %q: %w", name, err)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	key := types.NamespacedName{Name: name, Namespace: backupNamespace}
	for {
		var current velerov1.Backup
		if err := c.cl.Get(deadlineCtx, key, &current); err != nil {
			return fmt.Errorf("velero: backup get %q: %w", name, err)
		}
		switch current.Status.Phase {
		case velerov1.BackupPhaseCompleted:
			if current.Status.Errors != 0 {
				return fmt.Errorf("velero: backup %q completed with %d errors", name, current.Status.Errors)
			}
			return nil
		case velerov1.BackupPhaseFailed, velerov1.BackupPhasePartiallyFailed:
			return fmt.Errorf("velero: backup %q: phase %s", name, current.Status.Phase)
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("velero: backup %q: timeout after %s", name, timeout)
		case <-ticker.C:
		}
	}
}
