package core

import (
	"context"
	"fmt"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/hcloud"
	"k8s.io/client-go/kubernetes"
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

func ElasticAutoscalerErr() error {
	return fmt.Errorf("the cluster-autoscaler owns the elastic pool; horizon does not provision elastic nodes")
}

func secretRef(r config.SecretRef) hcloud.SecretRef {
	return hcloud.SecretRef{Namespace: r.Namespace, Name: r.Name, Key: r.Key}
}

func ReservedSpec(ctx context.Context, kc kubernetes.Interface, cfg config.Reserved) (*hcloud.Client, hcloud.ServerSpec, error) {
	token, err := hcloud.ReadToken(ctx, kc, secretRef(cfg.Token))
	if err != nil {
		return nil, hcloud.ServerSpec{}, err
	}
	join, err := hcloud.ReadJoinMaterial(ctx, kc, secretRef(cfg.JoinConfig))
	if err != nil {
		return nil, hcloud.ServerSpec{}, err
	}
	userData, err := hcloud.BuildUserData(hcloud.UserDataInput{
		ElasticCloudInit: join.ElasticCloudInit,
		ElasticPoolValue: join.ElasticPoolValue,
	})
	if err != nil {
		return nil, hcloud.ServerSpec{}, err
	}
	client, err := hcloud.NewClient(token)
	if err != nil {
		return nil, hcloud.ServerSpec{}, err
	}
	spec := hcloud.ServerSpec{
		Location:   cfg.Location,
		ServerType: cfg.ServerType,
		SSHKeys:    cfg.SSHKeys,
		UserData:   userData,
	}
	return client, spec, nil
}

func ScaleUp(ctx context.Context, hc *hcloud.Client, spec hcloud.ServerSpec, target PoolTarget, dryRun bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.PoolType == ElasticPoolType {
		return ElasticAutoscalerErr()
	}

	current, err := hc.ListReservedServers(ctx)
	if err != nil {
		return err
	}
	have := int32(len(current))

	if dryRun {
		progress.Emit(fmt.Sprintf("[dry-run] reserved pool: %d -> %d servers", have, target.Replicas))
		progress.Emit("[dry-run] No actions executed.")
		return nil
	}

	if have >= target.Replicas {
		progress.Emit(fmt.Sprintf("reserved pool already at %d servers (>= %d); nothing to do", have, target.Replicas))
		return nil
	}

	progress.Debug(fmt.Sprintf("scaling reserved servers %d -> %d", have, target.Replicas))
	if _, err := hc.ScaleReservedTo(ctx, spec, int(target.Replicas)); err != nil {
		return err
	}
	progress.Emit(fmt.Sprintf("Scaled reserved pool: %d -> %d servers", have, target.Replicas))
	return nil
}

func ScaleDown(ctx context.Context, hc *hcloud.Client, spec hcloud.ServerSpec, target PoolTarget, dryRun bool, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.PoolType == ElasticPoolType {
		return ElasticAutoscalerErr()
	}

	current, err := hc.ListReservedServers(ctx)
	if err != nil {
		return err
	}
	have := int32(len(current))

	if dryRun {
		progress.Emit(fmt.Sprintf("[dry-run] reserved pool: %d -> 0 servers", have))
		progress.Emit("[dry-run] No actions executed.")
		return nil
	}

	if have == 0 {
		progress.Emit("reserved pool already at 0 servers; nothing to do")
		return nil
	}

	progress.Debug("scaling reserved servers -> 0")
	if _, err := hc.ScaleReservedTo(ctx, spec, 0); err != nil {
		return err
	}
	progress.Emit(fmt.Sprintf("Scaled reserved pool to 0 servers (was %d)", have))
	return nil
}
