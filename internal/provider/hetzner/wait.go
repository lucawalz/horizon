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
