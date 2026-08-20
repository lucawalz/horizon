package catalogue

import (
	"errors"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
)

const (
	testConfig = "hetzner"
	regionA    = "fake-a"
	regionB    = "fake-b"
)

func instanceType(name, region string) provider.InstanceType {
	return provider.InstanceType{
		Name:         name,
		Architecture: "x86",
		CPUType:      "shared",
		CPUCores:     2,
		MemoryBytes:  4_000_000_000,
		DiskBytes:    40_000_000_000,
		Region:       region,
		Available:    true,
		HourlyRate:   provider.Rate{Amount: 0.006, Currency: "EUR"},
	}
}

func names(types []provider.InstanceType) []string {
	out := make([]string, 0, len(types))
	for _, it := range types {
		out = append(out, it.Name)
	}
	return out
}

func TestListReportsTheCatalogueUnavailableBeforeTheFirstFill(t *testing.T) {
	cache := NewCache()

	_, err := cache.List(testConfig, regionA)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("List of an unfilled cache = %v, want %v", err, ErrUnavailable)
	}
}

func TestListReportsAFilledCacheWithNoRowsInTheRegionAsAvailable(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})

	got, err := cache.List(testConfig, regionB)
	if err != nil {
		t.Fatalf("List of a filled cache in an empty region = %v, want no error", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want no rows", names(got))
	}
}

func TestListReturnsOnlyTheRowsOfTheRequestedRegion(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{
		instanceType("small", regionA),
		instanceType("large", regionB),
		instanceType("medium", regionA),
	})

	got, err := cache.List(testConfig, regionA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"small", "medium"}
	if len(got) != len(want) {
		t.Fatalf("List = %v, want %v", names(got), want)
	}
	for i, it := range got {
		if it.Name != want[i] || it.Region != regionA {
			t.Errorf("row %d = %+v, want %q in %q", i, it, want[i], regionA)
		}
	}
}

func TestListIsScopedToOneProviderConfig(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})

	if _, err := cache.List("other", regionA); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("List of an unknown provider config = %v, want %v", err, ErrUnavailable)
	}
}

func TestListRejectsAnEmptyRegion(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})

	if _, err := cache.List(testConfig, ""); err == nil {
		t.Fatal("List must reject an empty region")
	}
}

func TestListHandsOutRowsCallersCannotMutate(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})

	got, err := cache.List(testConfig, regionA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got[0].Name = "tampered"

	again, err := cache.List(testConfig, regionA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if again[0].Name != "small" {
		t.Errorf("cached row = %q, want %q", again[0].Name, "small")
	}
}

func TestStoreCopiesTheRowsItIsGiven(t *testing.T) {
	cache := NewCache()
	rows := []provider.InstanceType{instanceType("small", regionA)}
	cache.store(testConfig, rows)
	rows[0].Name = "tampered"

	got, err := cache.List(testConfig, regionA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Name != "small" {
		t.Errorf("cached row = %q, want %q", got[0].Name, "small")
	}
}

func TestAgeIsAbsentBeforeTheFirstFillAndTracksTheClockAfterIt(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	cache := NewCacheWithClock(func() time.Time { return now })

	if _, ok := cache.Age(testConfig); ok {
		t.Fatal("Age of an unfilled cache must report no snapshot")
	}

	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})
	now = now.Add(90 * time.Minute)

	age, ok := cache.Age(testConfig)
	if !ok {
		t.Fatal("Age of a filled cache must report a snapshot")
	}
	if age != 90*time.Minute {
		t.Errorf("Age = %s, want %s", age, 90*time.Minute)
	}
}

func TestRetainDropsTheEntriesOfProviderConfigsThatAreGone(t *testing.T) {
	cache := NewCache()
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})
	cache.store("other", []provider.InstanceType{instanceType("small", regionA)})

	cache.retain([]string{"other"})

	if _, err := cache.List(testConfig, regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List of a dropped provider config = %v, want %v", err, ErrUnavailable)
	}
	if _, err := cache.List("other", regionA); err != nil {
		t.Errorf("List of a retained provider config = %v, want no error", err)
	}
}
