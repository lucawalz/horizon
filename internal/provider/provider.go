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

// Provider is the lifecycle seam around a single cloud instance.
//
// Every implementation must satisfy the following contract, which the
// conformance suite in internal/provider/conformance enforces:
//
//   - Create is idempotent on Name. A request naming an instance that already
//     exists returns that instance rather than an error.
//   - Create applies Labels in the same call that creates the instance, never as
//     a later tagging step, so an instance is never unlabelled even momentarily.
//   - Get returns ErrNotFound, and no other error, when the instance is absent.
//   - Delete is idempotent and returns nil for an instance that is already
//     absent. Deletion counts as complete only once a subsequent Get reports
//     absence; a successful Delete call is not evidence on its own.
//   - Delete refuses any instance that does not carry
//     ManagedByLabelKey=ManagedByValue, and leaves it in place.
type Provider interface {
	Capabilities() Capabilities
	Create(ctx context.Context, req CreateRequest) (Instance, error)
	Get(ctx context.Context, name string) (Instance, error)
	List(ctx context.Context, selector map[string]string) ([]Instance, error)
	Delete(ctx context.Context, name string) error
}

// FormatExpiry encodes whole seconds because provider label values commonly
// reject the colons and plus signs that RFC 3339 timestamps carry.
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
