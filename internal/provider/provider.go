package provider

import "context"

const (
	PoolLabelKey      = "horizon.dev/pool"
	ReservedPoolValue = "reserved"
)

type Server struct {
	ID     int64
	Name   string
	Labels map[string]string
}

type Provider interface {
	ListReservedServers(ctx context.Context) ([]Server, error)
	ScaleReservedTo(ctx context.Context, want int) (int, error)
}
