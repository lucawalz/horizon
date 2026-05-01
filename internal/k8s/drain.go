package k8s

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

func Drain(ctx context.Context, kc kubernetes.Interface, nodeName string, timeout time.Duration) error {
	_ = drain.Helper{}
	panic("not implemented")
}
