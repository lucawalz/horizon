package core

import (
	"context"
	"io"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	"k8s.io/client-go/kubernetes"
)

func Drain(ctx context.Context, kc kubernetes.Interface, nodeName string, timeout time.Duration, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return k8s.Drain(ctx, kc, nodeName, timeout, out)
}
