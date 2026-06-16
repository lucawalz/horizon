package k8s

import (
	"context"
	"fmt"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

const evictRetryDelay = 5 * time.Second

func Drain(ctx context.Context, kc kubernetes.Interface, nodeName string, timeout time.Duration, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	node, err := kc.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("drain: get node %q: %w", nodeName, err)
	}

	helper := &drain.Helper{
		Ctx:                  ctx,
		Client:               kc,
		Force:                true,
		IgnoreAllDaemonSets:  true,
		DeleteEmptyDirData:   true,
		Timeout:              timeout,
		EvictErrorRetryDelay: evictRetryDelay,
		Out:                  out,
		ErrOut:               out,
	}

	if err := drain.RunCordonOrUncordon(helper, node, true); err != nil {
		return fmt.Errorf("drain: cordon %q: %w", nodeName, err)
	}

	if err := drain.RunNodeDrain(helper, nodeName); err != nil {
		return fmt.Errorf("drain: evict pods on %q: %w", nodeName, err)
	}

	return nil
}
