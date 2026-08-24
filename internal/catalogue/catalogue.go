// Package catalogue keeps the instance types offered by every provider config in memory.
package catalogue

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
)

const ReasonUnavailable = "catalogue_unavailable"

var ErrUnavailable = errors.New("catalogue: " + ReasonUnavailable)

var ErrEmpty = errors.New("catalogue: the provider offers no instance type in any region")

// an answer carrying nothing has delivered no catalogue, so every consumer treats it exactly as it treats a refusal
func FetchError(types []provider.InstanceType, err error) error {
	if err == nil && len(types) == 0 {
		return ErrEmpty
	}
	return err
}

type Reader interface {
	List(config, region string) ([]provider.InstanceType, error)
	Age(config string) (time.Duration, bool)
}

type snapshot struct {
	types     []provider.InstanceType
	fetchedAt time.Time
}

type Cache struct {
	now       func() time.Time
	mu        sync.RWMutex
	snapshots map[string]snapshot
}

var _ Reader = (*Cache)(nil)

func NewCache() *Cache {
	return NewCacheWithClock(time.Now)
}

func NewCacheWithClock(now func() time.Time) *Cache {
	return &Cache{now: now, snapshots: map[string]snapshot{}}
}

func (c *Cache) List(config, region string) ([]provider.InstanceType, error) {
	if region == "" {
		return nil, fmt.Errorf("catalogue: region is required")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	held, filled := c.snapshots[config]
	if !filled {
		return nil, fmt.Errorf("%w for provider config %q", ErrUnavailable, config)
	}

	out := make([]provider.InstanceType, 0, len(held.types))
	for _, it := range held.types {
		if it.Region == region {
			out = append(out, it)
		}
	}
	return out, nil
}

func (c *Cache) Age(config string) (time.Duration, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	held, filled := c.snapshots[config]
	if !filled {
		return 0, false
	}
	return c.now().Sub(held.fetchedAt), true
}

func (c *Cache) store(config string, types []provider.InstanceType) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshots[config] = snapshot{types: slices.Clone(types), fetchedAt: c.now()}
}

func (c *Cache) forget(config string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.snapshots, config)
}

func (c *Cache) retain(configs []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var evicted []string
	for config := range c.snapshots {
		if !slices.Contains(configs, config) {
			evicted = append(evicted, config)
			delete(c.snapshots, config)
		}
	}
	return evicted
}
