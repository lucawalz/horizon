package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const ElasticPoolType = "elastic"

type Progress func(msg string)

func (p Progress) emit(msg string) {
	if p != nil {
		p(msg)
	}
}

type PoolTarget struct {
	Namespace string
	Name      string
	PoolType  string
	Cluster   string
	Replicas  int32
}

func NotFoundPoolErr(namespace, name string) error {
	return fmt.Errorf("pool %s/%s not found; the home pool is provisioned via GitOps in bedrock, not by horizon",
		namespace, name)
}

func ScaleUp(ctx context.Context, cc *capi.Client, target PoolTarget, dryRun, nudge bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		md, err := cc.GetPool(ctx, target.Namespace, target.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return NotFoundPoolErr(target.Namespace, target.Name)
			}
			return fmt.Errorf("get pool: %w", err)
		}
		current := int32(0)
		if md.Spec.Replicas != nil {
			current = *md.Spec.Replicas
		}
		progress.emit(fmt.Sprintf("[dry-run] pool %s/%s (cluster %s): %d -> %d replicas",
			target.Namespace, target.Name, target.Cluster, current, target.Replicas))
		progress.emit("[dry-run] No actions executed.")
		return nil
	}

	initialized, err := cc.IsControlPlaneInitialized(ctx, target.Namespace, target.Cluster)
	if err != nil {
		return fmt.Errorf("control-plane status: %w", err)
	}
	if !initialized {
		if !nudge {
			return fmt.Errorf("control plane for cluster %q not initialized; rerun with --nudge to latch the externally-managed status", target.Cluster)
		}
		if err := cc.NudgeControlPlaneInitialized(ctx, target.Namespace, target.Cluster); err != nil {
			return err
		}
		progress.emit(fmt.Sprintf("Nudged control-plane-initialized for cluster %q.", target.Cluster))
	}

	md, err := cc.GetPool(ctx, target.Namespace, target.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return NotFoundPoolErr(target.Namespace, target.Name)
		}
		return fmt.Errorf("get pool: %w", err)
	}

	current := int32(0)
	if md.Spec.Replicas != nil {
		current = *md.Spec.Replicas
	}
	if current >= target.Replicas {
		progress.emit(fmt.Sprintf("pool %s/%s already at %d replicas (>= %d); nothing to do",
			target.Namespace, target.Name, current, target.Replicas))
		return nil
	}

	if err := cc.ScalePool(ctx, target.Namespace, target.Name, target.Replicas); err != nil {
		return err
	}
	progress.emit(fmt.Sprintf("Scaled pool %s/%s: %d -> %d replicas",
		target.Namespace, target.Name, current, target.Replicas))
	if target.PoolType == ElasticPoolType {
		progress.emit("Note: the cluster-autoscaler owns the elastic pool and may override this scale.")
	}
	return nil
}

func ScaleDown(ctx context.Context, cc *capi.Client, target PoolTarget, dryRun, del bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		if del {
			progress.emit(fmt.Sprintf("[dry-run] delete pool %s/%s", target.Namespace, target.Name))
		} else {
			progress.emit(fmt.Sprintf("[dry-run] scale pool %s/%s to 0 replicas", target.Namespace, target.Name))
		}
		progress.emit("[dry-run] No actions executed.")
		return nil
	}

	if del {
		if err := cc.DeletePool(ctx, target.Namespace, target.Name); err != nil {
			if apierrors.IsNotFound(err) {
				return NotFoundPoolErr(target.Namespace, target.Name)
			}
			return err
		}
		progress.emit(fmt.Sprintf("Deleted pool %s/%s", target.Namespace, target.Name))
		return nil
	}

	if err := cc.ScalePool(ctx, target.Namespace, target.Name, 0); err != nil {
		if apierrors.IsNotFound(err) {
			return NotFoundPoolErr(target.Namespace, target.Name)
		}
		return err
	}
	progress.emit(fmt.Sprintf("Scaled pool %s/%s to 0 replicas", target.Namespace, target.Name))
	return nil
}
