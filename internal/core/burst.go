package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lucawalz/horizon/internal/k8s"
	"github.com/lucawalz/horizon/internal/provider"
	"k8s.io/client-go/kubernetes"
)

const (
	burstNodePoll     = 5 * time.Second
	burstNodeTimeout  = 5 * time.Minute
	burstWorkloadPoll = 5 * time.Second
	burstWorkloadWait = 5 * time.Minute
	rollbackTimeout   = 30 * time.Second
)

type BurstParams struct {
	Target   PoolTarget
	Workload string
	PoolNode string
}

func Burst(ctx context.Context, prov provider.Provider, kc kubernetes.Interface, p BurstParams, progress Progress) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	prior, err := listReserved(ctx, prov)
	if err != nil {
		return fmt.Errorf("list reserved servers: %w", err)
	}
	priorCount := len(prior)

	scaled := false
	defer func() {
		if err == nil || !scaled {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = scaleReservedTo(rbCtx, prov, priorCount)
	}()

	want := int(p.Target.Replicas)
	progress.Debug(fmt.Sprintf("phase scale: reserved servers -> %d", want))
	if err := scaleReservedTo(ctx, prov, want); err != nil {
		return fmt.Errorf("scale reserved: %w", err)
	}
	scaled = true

	progress.Debug("phase wait: reserved nodes ready")
	if err := pollUntil(ctx, burstNodePoll, burstNodeTimeout, "wait nodes", func(c context.Context) (bool, error) {
		return k8s.ReservedNodesReady(c, kc, provider.ReservedPoolValue, want)
	}); err != nil {
		return err
	}

	progress.Debug("phase migrate: workload " + p.Workload)
	var migrated []string
	migrated, err = k8s.Migrate(ctx, kc, p.Workload, p.PoolNode)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if len(migrated) > 0 {
		progress.Debug("migrated: " + strings.Join(migrated, ", "))
	}
	defer func() {
		if err == nil {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_, _ = k8s.RestorePlacement(rbCtx, kc, p.Workload)
	}()

	if err = pollUntil(ctx, burstWorkloadPoll, burstWorkloadWait, "wait workload", func(c context.Context) (bool, error) {
		return k8s.WorkloadOnBurstNodes(c, kc, p.Workload)
	}); err != nil {
		return err
	}

	progress.Emit(fmt.Sprintf("Burst complete: reserved pool scaled to %d, workload %q migrated", want, p.Workload))
	return nil
}

func pollUntil(ctx context.Context, poll, timeout time.Duration, what string, check func(context.Context) (bool, error)) error {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		ok, err := check(pollCtx)
		if err == nil && ok {
			return nil
		}
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("%s: %w", what, ctx.Err())
			}
			if err != nil {
				return fmt.Errorf("%s: timeout after %s: %w", what, timeout, err)
			}
			return fmt.Errorf("%s: timeout after %s", what, timeout)
		case <-ticker.C:
		}
	}
}
