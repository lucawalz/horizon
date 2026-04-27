package provider

import "context"

type Provider interface {
	Apply(ctx context.Context, vars map[string]string) error
	Destroy(ctx context.Context) error
	Status(ctx context.Context) (string, error)
	GenerateTFVars() (map[string]string, error)
}
