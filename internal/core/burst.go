package core

import (
	"context"
	"fmt"
	"time"

	"github.com/lucawalz/horizon/internal/hcloud"
	"github.com/lucawalz/horizon/internal/k8s"
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

func Burst(ctx context.Context, hc *hcloud.Client, spec hcloud.ServerSpec, kc kubernetes.Interface, p BurstParams, progress Progress) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.Target.PoolType == ElasticPoolType {
		return ElasticAutoscalerErr()
	}

	prior, err := hc.ListReservedServers(ctx)
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
		_, _ = hc.ScaleReservedTo(rbCtx, spec, priorCount)
	}()

	want := int(p.Target.Replicas)
	progress.Debug(fmt.Sprintf("phase scale: reserved servers -> %d", want))
	if _, err := hc.ScaleReservedTo(ctx, spec, want); err != nil {
		return fmt.Errorf("scale reserved: %w", err)
	}
	scaled = true

	progress.Debug("phase wait: reserved nodes ready")
	if err := k8s.WaitReservedNodesReady(ctx, kc, hcloud.ReservedPoolValue, want, burstNodePoll, burstNodeTimeout); err != nil {
		return fmt.Errorf("wait nodes: %w", err)
	}

	progress.Debug("phase migrate: workload " + p.Workload)
	var saved *k8s.SavedState
	saved, err = k8s.Migrate(ctx, kc, p.Workload, p.PoolNode)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		rbCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = k8s.RollbackMigrate(rbCtx, kc, saved)
	}()

	if err = k8s.WaitWorkloadOnBurstNodes(ctx, kc, p.Workload, burstWorkloadPoll, burstWorkloadWait); err != nil {
		return fmt.Errorf("wait workload: %w", err)
	}

	progress.Emit(fmt.Sprintf("Burst complete: reserved pool scaled to %d, workload %q migrated", want, p.Workload))
	return nil
}
