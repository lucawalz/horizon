package hetzner

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"
)

func WaitNodeReady(ctx context.Context, kc kubernetes.Interface, hostname string, timeout, poll time.Duration) error {
	return nil
}
