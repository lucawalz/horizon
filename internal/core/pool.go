package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const ElasticPoolType = "elastic"

type Progress struct {
	emit  func(string)
	debug func(string)
}

func NewProgress(emit, debug func(string)) Progress {
	return Progress{emit: emit, debug: debug}
}

func (p Progress) Emit(s string) {
	if p.emit != nil {
		p.emit(s)
	}
}

func (p Progress) Debug(s string) {
	if p.debug != nil {
		p.debug(s)
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

func currentReplicas(md *clusterv1.MachineDeployment) int32 {
	if md.Spec.Replicas != nil {
		return *md.Spec.Replicas
	}
	return 0
}

func getPool(ctx context.Context, cc *capi.Client, target PoolTarget) (*clusterv1.MachineDeployment, error) {
	md, err := cc.GetPool(ctx, target.Namespace, target.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, NotFoundPoolErr(target.Namespace, target.Name)
		}
		return nil, fmt.Errorf("get pool: %w", err)
	}
	return md, nil
}

func ScaleUp(ctx context.Context, cc *capi.Client, target PoolTarget, dryRun bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		return scaleUpDryRun(ctx, cc, target, progress)
	}

	if err := ensureControlPlaneInitialized(ctx, cc, target); err != nil {
		return err
	}

	progress.Debug(fmt.Sprintf("pool type %q resolves to MachineDeployment %s/%s", target.PoolType, target.Namespace, target.Name))
	md, err := getPool(ctx, cc, target)
	if err != nil {
		return err
	}

	current := currentReplicas(md)
	progress.Debug(fmt.Sprintf("current replicas %d", current))
	if current >= target.Replicas {
		progress.Emit(fmt.Sprintf("pool %s/%s already at %d replicas (>= %d); nothing to do",
			target.Namespace, target.Name, current, target.Replicas))
		return nil
	}

	progress.Debug(fmt.Sprintf("patching replicas %d -> %d", current, target.Replicas))
	if err := cc.ScalePool(ctx, target.Namespace, target.Name, target.Replicas); err != nil {
		return err
	}
	progress.Debug("patch accepted")
	progress.Emit(fmt.Sprintf("Scaled pool %s/%s: %d -> %d replicas",
		target.Namespace, target.Name, current, target.Replicas))
	if target.PoolType == ElasticPoolType {
		progress.Emit("Note: the cluster-autoscaler owns the elastic pool and may override this scale.")
	}
	return nil
}

func scaleUpDryRun(ctx context.Context, cc *capi.Client, target PoolTarget, progress Progress) error {
	md, err := getPool(ctx, cc, target)
	if err != nil {
		return err
	}
	current := currentReplicas(md)
	progress.Emit(fmt.Sprintf("[dry-run] pool %s/%s (cluster %s): %d -> %d replicas",
		target.Namespace, target.Name, target.Cluster, current, target.Replicas))
	progress.Emit("[dry-run] No actions executed.")
	return nil
}

func ensureControlPlaneInitialized(ctx context.Context, cc *capi.Client, target PoolTarget) error {
	initialized, err := cc.IsControlPlaneInitialized(ctx, target.Namespace, target.Cluster)
	if err != nil {
		return fmt.Errorf("control-plane status: %w", err)
	}
	if !initialized {
		return fmt.Errorf("control plane for cluster %q not initialized; workers will not bootstrap until the control plane reports ready", target.Cluster)
	}
	return nil
}

func ScaleDown(ctx context.Context, cc *capi.Client, target PoolTarget, dryRun, del bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if dryRun {
		if del {
			progress.Emit(fmt.Sprintf("[dry-run] delete pool %s/%s", target.Namespace, target.Name))
		} else {
			progress.Emit(fmt.Sprintf("[dry-run] scale pool %s/%s to 0 replicas", target.Namespace, target.Name))
		}
		progress.Emit("[dry-run] No actions executed.")
		return nil
	}

	progress.Debug(fmt.Sprintf("pool type %q resolves to MachineDeployment %s/%s", target.PoolType, target.Namespace, target.Name))
	if del {
		progress.Debug("deleting MachineDeployment")
		if err := cc.DeletePool(ctx, target.Namespace, target.Name); err != nil {
			if apierrors.IsNotFound(err) {
				return NotFoundPoolErr(target.Namespace, target.Name)
			}
			return err
		}
		progress.Debug("delete accepted")
		progress.Emit(fmt.Sprintf("Deleted pool %s/%s", target.Namespace, target.Name))
		return nil
	}

	progress.Debug("patching replicas -> 0")
	if err := cc.ScalePool(ctx, target.Namespace, target.Name, 0); err != nil {
		if apierrors.IsNotFound(err) {
			return NotFoundPoolErr(target.Namespace, target.Name)
		}
		return err
	}
	progress.Debug("patch accepted")
	progress.Emit(fmt.Sprintf("Scaled pool %s/%s to 0 replicas", target.Namespace, target.Name))
	return nil
}
