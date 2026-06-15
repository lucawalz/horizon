package capi

import (
	"context"
	"fmt"
	"time"
)

func (c *Client) WaitMachinesReady(ctx context.Context, namespace, poolName string, want int32, poll, timeout time.Duration) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		names, err := c.MachineNodeNames(deadlineCtx, namespace, poolName)
		if err != nil {
			return err
		}
		if int32(len(names)) >= want {
			return nil
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("capi: pool %q: timeout after %s waiting for %d ready machines", poolName, timeout, want)
		case <-ticker.C:
		}
	}
}

func (c *Client) MachineNodeNames(ctx context.Context, namespace, poolName string) ([]string, error) {
	machines, err := c.ListMachines(ctx, namespace, poolName)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(machines))
	for _, m := range machines {
		if m.Status.NodeRef.Name != "" {
			names = append(names, m.Status.NodeRef.Name)
		}
	}
	return names, nil
}
