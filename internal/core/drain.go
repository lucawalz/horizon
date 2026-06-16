package core

import (
	"context"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	"k8s.io/client-go/kubernetes"
)

func Drain(ctx context.Context, kc kubernetes.Interface, nodeName string, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return k8s.Drain(ctx, kc, nodeName, timeout)
}
