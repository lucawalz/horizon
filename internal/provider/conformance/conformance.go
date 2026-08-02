package conformance

import (
	"errors"
	"maps"
	"testing"

	"github.com/lucawalz/horizon/internal/provider"
)

const (
	absentName    = "conformance-absent"
	instanceName  = "conformance-instance"
	unmanagedName = "conformance-unmanaged"
	otherPool     = "conformance-other-pool"
)

// SeedUnmanaged reaches behind the interface because no implementation will create an instance without the management label.
type Fixture struct {
	Provider      provider.Provider
	NewRequest    func(name string) provider.CreateRequest
	SeedUnmanaged func(name string) error
}

type Factory func(t *testing.T) Fixture

func Run(t *testing.T, newFixture Factory) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, f Fixture)
	}{
		{"CreateAppliesManagementLabel", createAppliesManagementLabel},
		{"CreateAppliesRequestedLabels", createAppliesRequestedLabels},
		{"CreateIsIdempotentOnName", createIsIdempotentOnName},
		{"GetReturnsNotFoundWhenAbsent", getReturnsNotFoundWhenAbsent},
		{"GetReturnsCreatedInstance", getReturnsCreatedInstance},
		{"ListMatchesSelector", listMatchesSelector},
		{"ListExcludesUnmatchedSelector", listExcludesUnmatchedSelector},
		{"DeleteMakesInstanceAbsent", deleteMakesInstanceAbsent},
		{"DeleteIsIdempotent", deleteIsIdempotent},
		{"DeleteAbsentInstanceSucceeds", deleteAbsentInstanceSucceeds},
		{"DeleteRefusesUnmanagedInstance", deleteRefusesUnmanagedInstance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newValidFixture(t, newFixture))
		})
	}
}

func newValidFixture(t *testing.T, newFixture Factory) Fixture {
	t.Helper()
	f := newFixture(t)
	if f.Provider == nil {
		t.Fatal("conformance fixture needs a Provider")
	}
	if f.NewRequest == nil {
		t.Fatal("conformance fixture needs a NewRequest")
	}
	if f.SeedUnmanaged == nil {
		t.Fatal("conformance fixture needs a SeedUnmanaged")
	}
	return f
}

func (f Fixture) request(name string, labels map[string]string) provider.CreateRequest {
	req := f.NewRequest(name)
	req.Name = name
	merged := maps.Clone(req.Labels)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, labels)
	req.Labels = merged
	return req
}

func mustCreate(t *testing.T, f Fixture, name string, labels map[string]string) provider.Instance {
	t.Helper()
	inst, err := f.Provider.Create(t.Context(), f.request(name, labels))
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	return inst
}

func reservedLabels() map[string]string {
	return map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue}
}

func createAppliesManagementLabel(t *testing.T, f Fixture) {
	inst := mustCreate(t, f, instanceName, reservedLabels())
	if inst.Name != instanceName {
		t.Errorf("created name = %q, want %q", inst.Name, instanceName)
	}
	if inst.ProviderID == "" {
		t.Error("created instance must carry a provider identifier")
	}
	if inst.Labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
		t.Errorf("created labels = %v, want %s=%s", inst.Labels, provider.ManagedByLabelKey, provider.ManagedByValue)
	}
}

func createAppliesRequestedLabels(t *testing.T, f Fixture) {
	if !f.Provider.Capabilities().SupportsResourceLabels {
		t.Skip("provider does not support resource labels")
	}
	mustCreate(t, f, instanceName, reservedLabels())

	got, err := f.Provider.Get(t.Context(), instanceName)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got.Labels[provider.PoolLabelKey] != provider.ReservedPoolValue {
		t.Errorf("labels = %v, want the requested labels applied by Create", got.Labels)
	}
}

func createIsIdempotentOnName(t *testing.T, f Fixture) {
	first := mustCreate(t, f, instanceName, reservedLabels())
	second := mustCreate(t, f, instanceName, reservedLabels())
	if second.ProviderID != first.ProviderID {
		t.Errorf("second Create returned %q, want the existing %q", second.ProviderID, first.ProviderID)
	}
}

func getReturnsNotFoundWhenAbsent(t *testing.T, f Fixture) {
	got, err := f.Provider.Get(t.Context(), absentName)
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Get of an absent instance = %v, want ErrNotFound", err)
	}
	if got.Name != "" || got.ProviderID != "" || len(got.Labels) != 0 {
		t.Errorf("Get of an absent instance returned %+v, want the zero instance", got)
	}
}

func getReturnsCreatedInstance(t *testing.T, f Fixture) {
	created := mustCreate(t, f, instanceName, reservedLabels())

	got, err := f.Provider.Get(t.Context(), instanceName)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got.Name != created.Name || got.ProviderID != created.ProviderID {
		t.Errorf("Get = %+v, want the created %+v", got, created)
	}
}

func listMatchesSelector(t *testing.T, f Fixture) {
	mustCreate(t, f, instanceName, reservedLabels())

	got, err := f.Provider.List(t.Context(), reservedLabels())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsName(got, instanceName) {
		t.Errorf("List = %+v, want it to contain %q", got, instanceName)
	}
}

func listExcludesUnmatchedSelector(t *testing.T, f Fixture) {
	mustCreate(t, f, instanceName, reservedLabels())

	got, err := f.Provider.List(t.Context(), map[string]string{provider.PoolLabelKey: otherPool})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if containsName(got, instanceName) {
		t.Errorf("List = %+v, want %q excluded by the selector", got, instanceName)
	}
}

func deleteMakesInstanceAbsent(t *testing.T, f Fixture) {
	mustCreate(t, f, instanceName, reservedLabels())

	if err := f.Provider.Delete(t.Context(), instanceName); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.Provider.Get(t.Context(), instanceName); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func deleteIsIdempotent(t *testing.T, f Fixture) {
	mustCreate(t, f, instanceName, reservedLabels())

	if err := f.Provider.Delete(t.Context(), instanceName); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := f.Provider.Delete(t.Context(), instanceName); err != nil {
		t.Fatalf("second Delete: %v, want nil", err)
	}
}

func deleteAbsentInstanceSucceeds(t *testing.T, f Fixture) {
	if err := f.Provider.Delete(t.Context(), absentName); err != nil {
		t.Fatalf("Delete of an absent instance = %v, want nil", err)
	}
}

func deleteRefusesUnmanagedInstance(t *testing.T, f Fixture) {
	if err := f.SeedUnmanaged(unmanagedName); err != nil {
		t.Fatalf("seed unmanaged instance: %v", err)
	}

	if err := f.Provider.Delete(t.Context(), unmanagedName); err == nil {
		t.Fatal("Delete of an unmanaged instance must be refused")
	}
	if _, err := f.Provider.Get(t.Context(), unmanagedName); err != nil {
		t.Fatalf("a refused Delete must leave the instance in place, Get = %v", err)
	}
}

func containsName(instances []provider.Instance, name string) bool {
	for _, inst := range instances {
		if inst.Name == name {
			return true
		}
	}
	return false
}
