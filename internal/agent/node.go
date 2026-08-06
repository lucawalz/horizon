package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/provider"
)

const nodeReadTimeout = 10 * time.Second

type nodeDeadline struct {
	kubeconfigPath string
	nodeName       string
	client         kubernetes.Interface
	last           *time.Time
}

func newNodeDeadline(kubeconfigPath, nodeName string, seed *time.Time) *nodeDeadline {
	return &nodeDeadline{kubeconfigPath: kubeconfigPath, nodeName: nodeName, last: seed}
}

func (d *nodeDeadline) read(ctx context.Context) *time.Time {
	deadline, err := d.fetch(ctx)
	if err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "read the wall clock deadline", "node", d.nodeName)
		return d.last
	}
	if deadline != nil {
		d.last = deadline
	}
	return d.last
}

func (d *nodeDeadline) fetch(ctx context.Context) (*time.Time, error) {
	client, err := d.connect()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, nodeReadTimeout)
	defer cancel()

	node, err := client.CoreV1().Nodes().Get(ctx, d.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("agent: get node %q: %w", d.nodeName, err)
	}

	raw, annotated := node.Annotations[provider.WatchdogDeadlineAnnotationKey]
	if !annotated {
		return nil, nil
	}
	deadline, readable := provider.ParseExpiryValue(raw)
	if !readable {
		return nil, fmt.Errorf("agent: node %q carries the unreadable deadline %q", d.nodeName, raw)
	}
	return &deadline, nil
}

func (d *nodeDeadline) markArmed(ctx context.Context, at time.Time) {
	if err := d.patchArmed(ctx, at); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "mark this node's watchdog armed", "node", d.nodeName)
	}
}

func (d *nodeDeadline) patchArmed(ctx context.Context, at time.Time) error {
	client, err := d.connect()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, nodeReadTimeout)
	defer cancel()

	patch, err := json.Marshal(map[string]map[string]map[string]string{
		"metadata": {"annotations": {provider.WatchdogArmedAnnotationKey: at.UTC().Format(time.RFC3339)}},
	})
	if err != nil {
		return fmt.Errorf("agent: build armed annotation patch for node %q: %w", d.nodeName, err)
	}
	if _, err := client.CoreV1().Nodes().Patch(ctx, d.nodeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("agent: patch node %q armed annotation: %w", d.nodeName, err)
	}
	return nil
}

func (d *nodeDeadline) connect() (kubernetes.Interface, error) {
	if d.client != nil {
		return d.client, nil
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", d.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("agent: read kubeconfig %q: %w", d.kubeconfigPath, err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("agent: build a kubernetes client from %q: %w", d.kubeconfigPath, err)
	}
	d.client = client
	return client, nil
}
