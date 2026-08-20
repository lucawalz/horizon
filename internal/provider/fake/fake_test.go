package fake_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/conformance"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

var frozen = time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)

func clock() func() time.Time {
	return func() time.Time { return frozen }
}

func TestFakeSatisfiesTheProviderContract(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		p := fake.New()
		p.SeedInstanceType(provider.InstanceType{Name: "type-b", Region: "fake-a", Available: true})
		p.SeedInstanceType(provider.InstanceType{Name: "type-a", Region: "fake-a", Available: true})
		p.SeedInstanceType(provider.InstanceType{Name: "type-c", Region: "fake-b", Available: true})
		p.SeedInstanceType(provider.InstanceType{Name: "type-d", Region: "fake-a", Available: false})
		return conformance.Fixture{
			Provider: p,
			NewRequest: func(name string) provider.CreateRequest {
				return provider.CreateRequest{Name: name, Region: "fake-a", Size: "small"}
			},
			SeedUnmanaged: func(name string) error {
				p.Seed(provider.Instance{Name: name})
				return nil
			},
			InstanceTypeRegion:         "fake-a",
			ExcludedInstanceType:       "type-c",
			AvailableFalseInstanceType: "type-d",
		}
	})
}

func TestFakeRecordsEveryCall(t *testing.T) {
	p := fake.NewWithClock(clock())
	ctx := t.Context()

	if _, err := p.Create(ctx, provider.CreateRequest{Name: "a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := p.Get(ctx, "a"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := p.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if calls := p.CreateCalls(); len(calls) != 1 || calls[0].Name != "a" {
		t.Errorf("create calls = %+v", calls)
	}
	if calls := p.GetCalls(); len(calls) != 1 || calls[0] != "a" {
		t.Errorf("get calls = %+v", calls)
	}
	if calls := p.DeleteCalls(); len(calls) != 1 || calls[0] != "a" {
		t.Errorf("delete calls = %+v", calls)
	}
}

func TestFakeInjectedFailuresSurface(t *testing.T) {
	boom := errors.New("boom")
	p := fake.NewWithClock(clock())
	p.FailCreate = func(string) error { return boom }
	p.FailGet = func(string) error { return boom }
	p.FailDelete = func(string) error { return boom }

	ctx := t.Context()
	if _, err := p.Create(ctx, provider.CreateRequest{Name: "a"}); !errors.Is(err, boom) {
		t.Errorf("Create error = %v, want boom", err)
	}
	if _, err := p.Get(ctx, "a"); !errors.Is(err, boom) {
		t.Errorf("Get error = %v, want boom", err)
	}
	if err := p.Delete(ctx, "a"); !errors.Is(err, boom) {
		t.Errorf("Delete error = %v, want boom", err)
	}
}

func TestFakeInjectedFailureCanTargetOneName(t *testing.T) {
	boom := errors.New("boom")
	p := fake.NewWithClock(clock())
	p.FailCreate = func(name string) error {
		if name == "b" {
			return boom
		}
		return nil
	}

	ctx := t.Context()
	if _, err := p.Create(ctx, provider.CreateRequest{Name: "a"}); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if _, err := p.Create(ctx, provider.CreateRequest{Name: "b"}); !errors.Is(err, boom) {
		t.Errorf("Create b = %v, want boom", err)
	}
}

func TestLedgerRecordsCreatesAndDeletes(t *testing.T) {
	p := fake.NewWithClock(clock())
	ctx := context.Background()

	if _, err := p.Create(ctx, expiring("a", frozen.Add(time.Hour))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := p.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	events := p.Ledger.Events()
	if len(events) != 2 {
		t.Fatalf("events = %v, want a create and a delete", events)
	}
	if events[0].Kind != fake.EventCreate || events[1].Kind != fake.EventDelete {
		t.Errorf("events = %v, want create then delete", events)
	}
	if !events[0].At.Equal(frozen) {
		t.Errorf("create timestamp = %v, want %v", events[0].At, frozen)
	}
	if !events[0].ExpiresAt.Equal(frozen.Add(time.Hour)) {
		t.Errorf("create expiry = %v, want %v", events[0].ExpiresAt, frozen.Add(time.Hour))
	}
}

func TestLedgerReportsNoLeakWhileTheDeadlineHolds(t *testing.T) {
	p := fake.NewWithClock(clock())

	if _, err := p.Create(context.Background(), expiring("a", frozen.Add(time.Hour))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if leaks := p.Ledger.Leaks(); len(leaks) != 0 {
		t.Errorf("leaks = %v, want none while the deadline is in the future", leaks)
	}
}

func TestLedgerReportsExpiredInstanceThatWasNeverDeleted(t *testing.T) {
	p := fake.NewWithClock(clock())

	if _, err := p.Create(context.Background(), expiring("a", frozen.Add(-time.Minute))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	leaks := p.Ledger.Leaks()
	if len(leaks) != 1 || leaks[0].Name != "a" {
		t.Fatalf("leaks = %v, want the expired instance a", leaks)
	}
}

func TestLedgerReportsNoLeakAfterDelete(t *testing.T) {
	p := fake.NewWithClock(clock())
	ctx := context.Background()

	if _, err := p.Create(ctx, expiring("a", frozen.Add(-time.Minute))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := p.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if leaks := p.Ledger.Leaks(); len(leaks) != 0 {
		t.Errorf("leaks = %v, want none after the instance was deleted", leaks)
	}
}

func TestLedgerReportsInstanceCreatedWithoutADeadline(t *testing.T) {
	p := fake.NewWithClock(clock())

	if _, err := p.Create(context.Background(), provider.CreateRequest{Name: "a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	leaks := p.Ledger.Leaks()
	if len(leaks) != 1 || leaks[0].Name != "a" {
		t.Fatalf("leaks = %v, want an undeadlined instance reported", leaks)
	}
}

func TestSeededInstanceIsNotLedgered(t *testing.T) {
	p := fake.NewWithClock(clock())
	p.Seed(provider.Instance{Name: "pre-existing"})

	if events := p.Ledger.Events(); len(events) != 0 {
		t.Errorf("events = %v, want seeding to stay off the ledger", events)
	}
}

func TestListInstanceTypesRejectsAnEmptyRegion(t *testing.T) {
	p := fake.New()
	if _, err := p.ListInstanceTypes(context.Background(), ""); err == nil {
		t.Fatal("ListInstanceTypes must reject an empty region")
	}
}

func TestListInstanceTypesFiltersByRegionAndSortsByName(t *testing.T) {
	p := fake.New()
	p.SeedInstanceType(provider.InstanceType{Name: "type-b", Region: "fake-a"})
	p.SeedInstanceType(provider.InstanceType{Name: "type-a", Region: "fake-a"})
	p.SeedInstanceType(provider.InstanceType{Name: "type-c", Region: "fake-b"})

	got, err := p.ListInstanceTypes(context.Background(), "fake-a")
	if err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "type-a" || got[1].Name != "type-b" {
		t.Fatalf("got %+v, want [type-a type-b] sorted by name", got)
	}
}

func expiring(name string, deadline time.Time) provider.CreateRequest {
	return provider.CreateRequest{
		Name:   name,
		Labels: map[string]string{provider.ExpiresAtLabelKey: provider.FormatExpiry(deadline)},
	}
}
