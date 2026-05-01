package velero

import (
	"context"
	"time"

	crClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Client struct {
	client crClient.Client
}

func NewClient(kubeconfigPath string) (*Client, error) {
	panic("not implemented")
}

func NewClientWithCRClient(cl crClient.Client) *Client {
	return &Client{client: cl}
}

func (c *Client) TriggerBackup(ctx context.Context, namespace, name string, poll, timeout time.Duration) error {
	panic("not implemented")
}
