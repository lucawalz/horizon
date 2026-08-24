package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
)

type Provider struct {
	AdvertisedCapabilities provider.Capabilities
	FailCreate             func(name string) error
	FailGet                func(name string) error
	FailDelete             func(name string) error
	FailListInstanceTypes  func(region string) error
	Ledger                 *Ledger

	now           func() time.Time
	mu            sync.Mutex
	instances     map[string]provider.Instance
	instanceTypes []provider.InstanceType
	nextID        int64
	createCalls   []provider.CreateRequest
	getCalls      []string
	deleteCalls   []string
}

var _ provider.Provider = (*Provider)(nil)

func New() *Provider {
	return NewWithClock(time.Now)
}

func NewWithClock(now func() time.Time) *Provider {
	return &Provider{
		AdvertisedCapabilities: provider.Capabilities{
			SelfTerminationStopsBilling: false,
			SupportsResourceLabels:      true,
			Regions:                     []string{"fake-a", "fake-b"},
		},
		Ledger:    newLedger(now),
		now:       now,
		instances: map[string]provider.Instance{},
	}
}

func (p *Provider) Seed(inst provider.Instance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if inst.ProviderID == "" {
		p.nextID++
		inst.ProviderID = fmt.Sprintf("fake://%d", p.nextID)
	}
	if inst.State == "" {
		inst.State = provider.Running
	}
	if inst.CreatedAt.IsZero() {
		inst.CreatedAt = p.now()
	}
	inst.Labels = maps.Clone(inst.Labels)
	p.instances[inst.Name] = inst
}

func (p *Provider) CreateCalls() []provider.CreateRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.createCalls)
}

func (p *Provider) GetCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.getCalls)
}

func (p *Provider) DeleteCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.deleteCalls)
}

func (p *Provider) Capabilities() provider.Capabilities {
	return p.AdvertisedCapabilities
}

func (p *Provider) Create(_ context.Context, req provider.CreateRequest) (provider.Instance, error) {
	record(&p.mu, &p.createCalls, cloneRequest(req))
	if err := inject(p.FailCreate, req.Name); err != nil {
		return provider.Instance{}, err
	}
	if req.Name == "" {
		return provider.Instance{}, fmt.Errorf("fake: instance name is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.instances[req.Name]; ok {
		return cloneInstance(existing), nil
	}
	p.nextID++
	labels := maps.Clone(req.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[provider.ManagedByLabelKey] = provider.ManagedByValue
	created := p.now()
	inst := provider.Instance{
		Name:       req.Name,
		ProviderID: fmt.Sprintf("fake://%d", p.nextID),
		Region:     req.Region,
		Size:       req.Size,
		State:      provider.Running,
		Labels:     labels,
		CreatedAt:  created,
	}
	p.instances[inst.Name] = inst
	p.Ledger.record(EventCreate, inst, created)
	return cloneInstance(inst), nil
}

func (p *Provider) Get(_ context.Context, name string) (provider.Instance, error) {
	record(&p.mu, &p.getCalls, name)
	if err := inject(p.FailGet, name); err != nil {
		return provider.Instance{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	inst, ok := p.instances[name]
	if !ok {
		return provider.Instance{}, fmt.Errorf("fake: instance %q: %w", name, provider.ErrNotFound)
	}
	return cloneInstance(inst), nil
}

func (p *Provider) List(_ context.Context, selector map[string]string) ([]provider.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.Instance, 0, len(p.instances))
	for _, inst := range p.instances {
		if inst.Labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
			continue
		}
		if !matches(inst.Labels, selector) {
			continue
		}
		out = append(out, cloneInstance(inst))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (p *Provider) Delete(_ context.Context, name string) error {
	record(&p.mu, &p.deleteCalls, name)
	if err := inject(p.FailDelete, name); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	inst, ok := p.instances[name]
	if !ok {
		return nil
	}
	if inst.Labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
		return fmt.Errorf("fake: refusing to delete instance %q: not labelled %s=%s",
			name, provider.ManagedByLabelKey, provider.ManagedByValue)
	}
	delete(p.instances, name)
	p.Ledger.record(EventDelete, inst, p.now())
	return nil
}

func record[T any](mu *sync.Mutex, calls *[]T, call T) {
	mu.Lock()
	defer mu.Unlock()
	*calls = append(*calls, call)
}

func inject(hook func(name string) error, name string) error {
	if hook == nil {
		return nil
	}
	return hook(name)
}

func matches(labels, selector map[string]string) bool {
	for key, want := range selector {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func cloneInstance(inst provider.Instance) provider.Instance {
	inst.Labels = maps.Clone(inst.Labels)
	return inst
}

func cloneRequest(req provider.CreateRequest) provider.CreateRequest {
	req.Labels = maps.Clone(req.Labels)
	return req
}

func (p *Provider) SeedInstanceType(it provider.InstanceType) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.instanceTypes = append(p.instanceTypes, it)
}

func (p *Provider) ForgetInstanceTypes() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.instanceTypes = nil
}

func (p *Provider) ListInstanceTypes(_ context.Context, region string) ([]provider.InstanceType, error) {
	if err := inject(p.FailListInstanceTypes, region); err != nil {
		return nil, err
	}
	if region == "" {
		return nil, fmt.Errorf("fake: instance type region is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.InstanceType, 0, len(p.instanceTypes))
	for _, it := range p.instanceTypes {
		if it.Region != region {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
