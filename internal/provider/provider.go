package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	PoolLabelKey      = "horizon.dev/pool"
	ReservedPoolValue = "reserved"
	ManagedByLabelKey = "horizon.dev/managed-by"
	ManagedByValue    = "horizon"
	ExpiresAtLabelKey = "horizon.dev/expires-at"

	LeaseUIDLabelKey = "horizon.dev/lease-uid"
)

var ErrNotFound = errors.New("provider: instance not found")

type InstanceState string

const (
	Provisioning InstanceState = "Provisioning"
	Running      InstanceState = "Running"
	Terminating  InstanceState = "Terminating"
)

type Capabilities struct {
	SelfTerminationStopsBilling bool
	SupportsResourceLabels      bool
	Regions                     []string
}

type Instance struct {
	Name       string
	ProviderID string
	Region     string
	State      InstanceState
	Labels     map[string]string
	CreatedAt  time.Time
}

type CreateRequest struct {
	Name     string
	Region   string
	Size     string
	Labels   map[string]string
	UserData string
}

type Provider interface {
	Capabilities() Capabilities
	Create(ctx context.Context, req CreateRequest) (Instance, error)
	Get(ctx context.Context, name string) (Instance, error)
	List(ctx context.Context, selector map[string]string) ([]Instance, error)
	Delete(ctx context.Context, name string) error
}

func ConfirmAbsent(ctx context.Context, p Provider, name string) (bool, error) {
	_, err := p.Get(ctx, name)
	switch {
	case errors.Is(err, ErrNotFound):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("provider: confirm instance %q is absent: %w", name, err)
	default:
		return false, nil
	}
}

func FormatExpiry(deadline time.Time) string {
	return strconv.FormatInt(deadline.UTC().Unix(), 10)
}

func ParseExpiry(labels map[string]string) (time.Time, bool) {
	raw, ok := labels[ExpiresAtLabelKey]
	if !ok {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}
