package hetzner

import (
	"context"

	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/provider"
	"k8s.io/client-go/kubernetes"
)

func Reserved(ctx context.Context, kc kubernetes.Interface, cfg config.Reserved) (provider.Provider, error) {
	token, err := cfg.Token.Resolve(ctx, kc)
	if err != nil {
		return nil, err
	}
	raw, err := cfg.CloudInit.Resolve(ctx, kc)
	if err != nil {
		return nil, err
	}
	spec := ServerSpec{
		Location:   cfg.Location,
		ServerType: cfg.ServerType,
		ImageLabel: cfg.Image.Label,
		ImageValue: cfg.Image.Value,
		SSHKeys:    cfg.SSHKeys,
		UserData:   raw,
	}
	return NewClient(token, spec)
}
