package provider

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const (
	PoolLabelKey      = "horizon.dev/pool"
	ReservedPoolValue = "reserved"
	ManagedByLabelKey = "horizon.dev/managed-by"
	ManagedByValue    = "horizon"
	ExpiresAtLabelKey = "horizon.dev/expires-at"
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
