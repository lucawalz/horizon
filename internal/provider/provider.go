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

	WatchdogDeadlineAnnotationKey = "horizon.dev/watchdog-deadline"

	LeaseUIDLabelKey = "horizon.dev/lease-uid"
)

const (
	SentinelPrefix = "${HORIZON_"
	SentinelSuffix = "}"
)

const (
	NodeTokenSentinel   = SentinelPrefix + "NODE_TOKEN" + SentinelSuffix
	VersionSentinel     = SentinelPrefix + "VERSION" + SentinelSuffix
	MaxLifetimeSentinel = SentinelPrefix + "MAX_LIFETIME" + SentinelSuffix
	JoinTokenSentinel   = SentinelPrefix + "JOIN_TOKEN" + SentinelSuffix
)

func Sentinels() []string {
	return []string{NodeTokenSentinel, VersionSentinel, MaxLifetimeSentinel, JoinTokenSentinel}
}

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
	return ParseExpiryValue(labels[ExpiresAtLabelKey])
}

func ParseExpiryValue(raw string) (time.Time, bool) {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}
