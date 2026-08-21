package metered

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/metrics/metricstest"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/conformance"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const (
	clockStep    = 250 * time.Millisecond
	instanceName = "metered-instance"
	testRegion   = "fake-a"
	testSize     = "small"
)

type testClock struct {
	at time.Time
}

func (c *testClock) now() time.Time {
	return c.at
}

func (c *testClock) tick() {
	c.at = c.at.Add(clockStep)
}

// only a call into the provider moves this clock, so a duration read outside the call reads zero
type slowProvider struct {
	inner provider.Provider
	tick  func()
}

func (s slowProvider) Capabilities() provider.Capabilities {
	return s.inner.Capabilities()
}

func (s slowProvider) Create(ctx context.Context, req provider.CreateRequest) (provider.Instance, error) {
	s.tick()
	return s.inner.Create(ctx, req)
}

func (s slowProvider) Get(ctx context.Context, name string) (provider.Instance, error) {
	s.tick()
	return s.inner.Get(ctx, name)
}

func (s slowProvider) List(ctx context.Context, selector map[string]string) ([]provider.Instance, error) {
	s.tick()
	return s.inner.List(ctx, selector)
}

func (s slowProvider) Delete(ctx context.Context, name string) error {
	s.tick()
	return s.inner.Delete(ctx, name)
}

func (s slowProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	s.tick()
	return s.inner.ListInstanceTypes(ctx, region)
}

func seededFake() *fake.Provider {
	inner := offeringFake()
	inner.Seed(provider.Instance{
		Name:   instanceName,
		Labels: map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue},
	})
	return inner
}

func offeringFake() *fake.Provider {
	inner := fake.New()
	inner.SeedInstanceType(provider.InstanceType{Name: testSize, Region: testRegion, Available: true})
	return inner
}

func timedProvider(config string, inner provider.Provider) *Provider {
	clock := &testClock{at: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}
	p := Wrap(config, slowProvider{inner: inner, tick: clock.tick})
	p.now = clock.now
	return p
}

func requestLabels(config string, operation metrics.Operation, result metrics.Result) map[string]string {
	return map[string]string{
		"provider":  config,
		"operation": string(operation),
		"result":    string(result),
	}
}

func assertObserved(t *testing.T, taken metricstest.Snapshot, labels map[string]string, wantCount uint64, wantSum float64) {
	t.Helper()
	count, sum := taken.Observations(t, metricstest.ProviderRequests, labels)
	if count != wantCount {
		t.Errorf("%v holds %d observations, want %d", labels, count, wantCount)
	}
	if sum != wantSum {
		t.Errorf("%v sums to %v seconds, want %v", labels, sum, wantSum)
	}
}

func TestTheDecoratorSatisfiesTheProviderContract(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		inner := fake.New()
		inner.SeedInstanceType(provider.InstanceType{Name: "type-b", Region: testRegion, Available: true})
		inner.SeedInstanceType(provider.InstanceType{Name: "type-a", Region: testRegion, Available: true})
		inner.SeedInstanceType(provider.InstanceType{Name: "type-c", Region: "fake-b", Available: true})
		inner.SeedInstanceType(provider.InstanceType{Name: "type-d", Region: testRegion, Available: false})
		return conformance.Fixture{
			Provider: Wrap("conformance", inner),
			NewRequest: func(name string) provider.CreateRequest {
				return provider.CreateRequest{Name: name, Region: testRegion, Size: testSize}
			},
			SeedUnmanaged: func(name string) error {
				inner.Seed(provider.Instance{Name: name})
				return nil
			},
			InstanceTypeRegion:         testRegion,
			ExcludedInstanceType:       "type-c",
			AvailableFalseInstanceType: "type-d",
		}
	})
}

func TestEveryProviderCallIsTimed(t *testing.T) {
	cases := []struct {
		name      string
		operation metrics.Operation
		call      func(context.Context, *Provider) error
	}{
		{"create", metrics.OperationCreate, func(ctx context.Context, p *Provider) error {
			_, err := p.Create(ctx, provider.CreateRequest{Name: instanceName, Region: testRegion, Size: testSize})
			return err
		}},
		{"get", metrics.OperationGet, func(ctx context.Context, p *Provider) error {
			_, err := p.Get(ctx, instanceName)
			return err
		}},
		{"list", metrics.OperationList, func(ctx context.Context, p *Provider) error {
			_, err := p.List(ctx, nil)
			return err
		}},
		{"delete", metrics.OperationDelete, func(ctx context.Context, p *Provider) error {
			return p.Delete(ctx, instanceName)
		}},
		{"list instance types", metrics.OperationListInstanceTypes, func(ctx context.Context, p *Provider) error {
			_, err := p.ListInstanceTypes(ctx, testRegion)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := "timed-" + string(tc.operation)
			taken := metricstest.Take(t)

			if err := tc.call(t.Context(), timedProvider(config, seededFake())); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			assertObserved(t, taken, requestLabels(config, tc.operation, metrics.ResultSuccess), 1, clockStep.Seconds())
		})
	}
}

func TestAFailedCallIsStillTimed(t *testing.T) {
	const config = "failing"
	inner := seededFake()
	inner.FailCreate = func(string) error { return errors.New("hetzner is down") }
	taken := metricstest.Take(t)

	if _, err := timedProvider(config, inner).Create(t.Context(), provider.CreateRequest{Name: instanceName}); err == nil {
		t.Fatal("the decorator swallowed the provider failure")
	}

	assertObserved(t, taken, requestLabels(config, metrics.OperationCreate, metrics.ResultFailure), 1, clockStep.Seconds())
}

func TestAnAbsentInstanceIsNotAProviderFailure(t *testing.T) {
	const config = "absent"
	taken := metricstest.Take(t)

	if _, err := timedProvider(config, seededFake()).Get(t.Context(), "never-created"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Get of an absent instance = %v, want ErrNotFound", err)
	}

	assertObserved(t, taken, requestLabels(config, metrics.OperationGet, metrics.ResultNotFound), 1, clockStep.Seconds())
	assertObserved(t, taken, requestLabels(config, metrics.OperationGet, metrics.ResultFailure), 0, 0)
}

func TestACancelledCallIsNotAProviderFailure(t *testing.T) {
	const config = "cancelled"
	inner := seededFake()
	inner.FailGet = func(name string) error { return fmt.Errorf("get %q: %w", name, context.Canceled) }
	taken := metricstest.Take(t)

	if _, err := timedProvider(config, inner).Get(t.Context(), instanceName); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v, want the cancellation passed through", err)
	}

	assertObserved(t, taken, requestLabels(config, metrics.OperationGet, metrics.ResultCanceled), 1, clockStep.Seconds())
	assertObserved(t, taken, requestLabels(config, metrics.OperationGet, metrics.ResultFailure), 0, 0)
}

func TestATimedOutCallIsAProviderFailure(t *testing.T) {
	const config = "timed-out"
	inner := seededFake()
	inner.FailGet = func(name string) error { return fmt.Errorf("get %q: %w", name, context.DeadlineExceeded) }
	taken := metricstest.Take(t)

	if _, err := timedProvider(config, inner).Get(t.Context(), instanceName); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get = %v, want the deadline passed through", err)
	}

	assertObserved(t, taken, requestLabels(config, metrics.OperationGet, metrics.ResultFailure), 1, clockStep.Seconds())
}

func TestCapabilitiesReachTheCallerWithoutARequestSeries(t *testing.T) {
	const config = "capabilities"
	inner := seededFake()
	inner.AdvertisedCapabilities.Regions = []string{testRegion}
	taken := metricstest.Take(t)

	got := timedProvider(config, inner).Capabilities()

	if len(got.Regions) != 1 || got.Regions[0] != testRegion {
		t.Errorf("Capabilities = %+v, want the inner provider's own", got)
	}
	for _, result := range []metrics.Result{metrics.ResultSuccess, metrics.ResultFailure} {
		for _, operation := range everyOperation() {
			assertObserved(t, taken, requestLabels(config, operation, result), 0, 0)
		}
	}
}

func TestTheDecoratorPassesEveryValueThrough(t *testing.T) {
	p := timedProvider("passthrough", offeringFake())
	ctx := t.Context()

	created, err := p.Create(ctx, provider.CreateRequest{Name: "passed", Region: testRegion, Size: testSize})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "passed" || created.Region != testRegion || created.Size != testSize {
		t.Errorf("Create = %+v, want the instance the inner provider built", created)
	}

	listed, err := p.List(ctx, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "passed" {
		t.Errorf("List = %+v, want the created instance", listed)
	}

	types, err := p.ListInstanceTypes(ctx, testRegion)
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if len(types) != 1 || types[0].Name != testSize {
		t.Errorf("ListInstanceTypes = %+v, want the seeded type", types)
	}
}

func TestNothingFromARequestReachesALabel(t *testing.T) {
	const unenumerable = "minted-by-a-request"
	p := timedProvider("cardinality", seededFake())
	ctx := t.Context()

	if _, err := p.Create(ctx, provider.CreateRequest{
		Name:   unenumerable,
		Region: unenumerable,
		Size:   unenumerable,
		Labels: map[string]string{unenumerable: unenumerable},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := p.Get(ctx, unenumerable); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := p.List(ctx, map[string]string{unenumerable: unenumerable}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := p.ListInstanceTypes(ctx, unenumerable); err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if err := p.Delete(ctx, unenumerable); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	metricstest.AssertNoSeriesCarries(t, unenumerable)
}

func everyOperation() []metrics.Operation {
	return []metrics.Operation{
		metrics.OperationCreate,
		metrics.OperationGet,
		metrics.OperationList,
		metrics.OperationDelete,
		metrics.OperationListInstanceTypes,
	}
}
