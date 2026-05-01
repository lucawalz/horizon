package hetzner

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func WaitNodeReady(ctx context.Context, kc kubernetes.Interface, hostname string, timeout, poll time.Duration) error {
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
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("hetzner: wait %s: %w", hostname, ctx.Err())
			}
			if lastErr != nil {
				return fmt.Errorf("hetzner: wait %s: timeout: %w", hostname, lastErr)
			}
			return fmt.Errorf("hetzner: wait %s: timeout after %s", hostname, timeout)
		default:
		}

		ready, err := nodeIsReady(pollCtx, kc, hostname)
		if err != nil {
			lastErr = err
		} else if !ready {
			lastErr = fmt.Errorf("node %s not Ready", hostname)
		} else {
			flannelOK, err := flannelPodRunningOnNode(pollCtx, kc, hostname)
			if err != nil {
				lastErr = err
			} else if !flannelOK {
				lastErr = fmt.Errorf("flannel pod not Running on node %s", hostname)
			} else {
				return nil
			}
		}

		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("hetzner: wait %s: %w", hostname, ctx.Err())
			}
			if lastErr != nil {
				return fmt.Errorf("hetzner: wait %s: timeout: %w", hostname, lastErr)
			}
			return fmt.Errorf("hetzner: wait %s: timeout after %s", hostname, timeout)
		case <-ticker.C:
		}
	}
}

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
	return kc.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
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
				ready := false
				for _, c := range n.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
						ready = true
						break
					}
				}
				if !ready {
					lastErr = fmt.Errorf("node %s not Ready", n.Name)
					break
				}
				ok, err := flannelPodRunningOnNode(pollCtx, kc, n.Name)
				if err != nil {
					lastErr = err
					break
				}
				if !ok {
					lastErr = fmt.Errorf("flannel pod not Running on node %s", n.Name)
					break
				}
				return n.Name, nil
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

func flannelPodRunningOnNode(ctx context.Context, kc kubernetes.Interface, nodeName string) (bool, error) {
	pods, err := kc.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=flannel",
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return false, fmt.Errorf("list flannel pods on %s: %w", nodeName, err)
	}
	for _, p := range pods.Items {
		if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
			return true, nil
		}
	}
	return false, nil
}
