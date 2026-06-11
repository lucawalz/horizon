package hetzner

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func ListNodeNames(ctx context.Context, kc kubernetes.Interface) (map[string]bool, error) {
	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("hetzner: list nodes: %w", err)
	}
	names := make(map[string]bool, len(nodes.Items))
	for _, n := range nodes.Items {
		names[n.Name] = true
	}
	return names, nil
}

func DeleteNode(ctx context.Context, kc kubernetes.Interface, name string) error {
	err := kc.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}

func WaitNewNodeReady(ctx context.Context, kc kubernetes.Interface, exclude map[string]bool, timeout, poll time.Duration) (string, error) {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	var found string
	var lastErr error

	for {
		nodes, err := kc.CoreV1().Nodes().List(pollCtx, metav1.ListOptions{})
		if err != nil {
			lastErr = fmt.Errorf("list nodes: %w", err)
		} else {
			for _, n := range nodes.Items {
				if exclude[n.Name] {
					continue
				}
				found = n.Name
				for _, c := range n.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
						return n.Name, nil
					}
				}
				lastErr = fmt.Errorf("node %s not Ready", n.Name)
			}
			if found == "" {
				lastErr = fmt.Errorf("no new node appeared yet")
			}
		}
		select {
		case <-pollCtx.Done():
			if lastErr != nil {
				return found, fmt.Errorf("hetzner: wait new node: timeout: %w", lastErr)
			}
			return found, fmt.Errorf("hetzner: wait new node: timeout after %s", timeout)
		case <-ticker.C:
		}
	}
}

func WaitNodeReady(ctx context.Context, kc kubernetes.Interface, name string, timeout, poll time.Duration) error {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	var lastErr error
	for {
		ready, err := nodeIsReady(pollCtx, kc, name)
		if err != nil {
			lastErr = err
		} else if ready {
			return nil
		} else {
			lastErr = fmt.Errorf("node %s not Ready", name)
		}
		select {
		case <-pollCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("hetzner: wait node %s ready: timeout: %w", name, lastErr)
			}
			return fmt.Errorf("hetzner: wait node %s ready: timeout after %s", name, timeout)
		case <-ticker.C:
		}
	}
}

func nodeIsReady(ctx context.Context, kc kubernetes.Interface, name string) (bool, error) {
	n, err := kc.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("get node %s: %w", name, err)
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}

