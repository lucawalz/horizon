package cli

import (
	"context"

	"k8s.io/client-go/kubernetes"
)

func RunDrainForTest(ctx context.Context, kc kubernetes.Interface, nodeName string) error {
	panic("not implemented")
}
