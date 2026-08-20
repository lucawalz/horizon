package catalogue

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	providerfake "github.com/lucawalz/horizon/internal/provider/fake"
)

var (
	_ manager.Runnable               = (*Refresher)(nil)
	_ manager.LeaderElectionRunnable = (*Refresher)(nil)
)

func scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func providerConfig(name string) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func apiClient(configs ...*v1alpha1.ProviderConfig) client.Client {
	builder := clientfake.NewClientBuilder().WithScheme(scheme())
	for _, cfg := range configs {
		builder = builder.WithObjects(cfg)
	}
	return builder.Build()
}

func seededProvider(regions ...string) *providerfake.Provider {
	prov := providerfake.New()
	prov.AdvertisedCapabilities.Regions = regions
	for _, region := range regions {
		prov.SeedInstanceType(instanceType("small", region))
	}
	return prov
}

func staticFactory(prov Lister) ListerFactory {
	return func(context.Context, *v1alpha1.ProviderConfig) (Lister, error) { return prov, nil }
}

func TestRefreshFillsTheCacheFromEveryRegionTheProviderOffers(t *testing.T) {
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig(testConfig)),
		Lister: staticFactory(seededProvider(regionA, regionB)),
		Cache:  cache,
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}

	for _, region := range []string{regionA, regionB} {
		got, err := cache.List(testConfig, region)
		if err != nil {
			t.Fatalf("List %q: %v", region, err)
		}
		if len(got) != 1 || got[0].Region != region {
			t.Errorf("List %q = %+v, want one row of that region", region, got)
		}
	}
}

func TestRefreshServesThePreviousSnapshotWhenTheProviderFails(t *testing.T) {
	prov := seededProvider(regionA)
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig(testConfig)),
		Lister: staticFactory(prov),
		Cache:  cache,
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("first refreshAll: %v", err)
	}

	prov.FailListInstanceTypes = func(string) error { return errors.New("hetzner is down") }
	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	got, err := cache.List(testConfig, regionA)
	if err != nil {
		t.Fatalf("List after a failed refresh = %v, want the previous snapshot", err)
	}
	if len(got) != 1 || got[0].Name != "small" {
		t.Errorf("List after a failed refresh = %v, want the previous snapshot", names(got))
	}

	counts := refresher.Counts()
	if counts.Success != 1 || counts.Failure != 1 {
		t.Errorf("Counts = %+v, want one success and one failure", counts)
	}
}

func TestRefreshLeavesTheCatalogueUnavailableWhenTheFirstFetchFails(t *testing.T) {
	prov := seededProvider(regionA)
	prov.FailListInstanceTypes = func(string) error { return errors.New("hetzner is down") }
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig(testConfig)),
		Lister: staticFactory(prov),
		Cache:  cache,
	}

	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	if _, err := cache.List(testConfig, regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List after a refresh that never filled = %v, want %v", err, ErrUnavailable)
	}
	if _, ok := cache.Age(testConfig); ok {
		t.Error("Age must report no snapshot after a refresh that never filled")
	}
}

func TestRefreshKeepsAPartialSnapshotOutOfTheCache(t *testing.T) {
	prov := seededProvider(regionA, regionB)
	prov.FailListInstanceTypes = func(region string) error {
		if region == regionB {
			return errors.New("region is unreachable")
		}
		return nil
	}
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig(testConfig)),
		Lister: staticFactory(prov),
		Cache:  cache,
	}

	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	if _, err := cache.List(testConfig, regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List = %v, want the half built snapshot discarded", err)
	}
}

func TestRefreshFillsEveryProviderConfigEvenWhenOneFails(t *testing.T) {
	broken := seededProvider(regionA)
	broken.FailListInstanceTypes = func(string) error { return errors.New("token is revoked") }
	healthy := seededProvider(regionA)

	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig("broken"), providerConfig("healthy")),
		Lister: func(_ context.Context, cfg *v1alpha1.ProviderConfig) (Lister, error) {
			if cfg.Name == "broken" {
				return broken, nil
			}
			return healthy, nil
		},
		Cache: cache,
	}

	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	if _, err := cache.List("healthy", regionA); err != nil {
		t.Errorf("List of the healthy provider config = %v, want its snapshot", err)
	}
	if _, err := cache.List("broken", regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List of the broken provider config = %v, want %v", err, ErrUnavailable)
	}
}

func TestRefreshDropsProviderConfigsThatAreGone(t *testing.T) {
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  cache,
	}
	cache.store("deleted", []provider.InstanceType{instanceType("small", regionA)})

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}

	if _, err := cache.List("deleted", regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List of a deleted provider config = %v, want %v", err, ErrUnavailable)
	}
}

func TestReconcileRefreshesOnlyTheProviderConfigItIsGiven(t *testing.T) {
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(providerConfig(testConfig), providerConfig("other")),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  cache,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: testConfig}}
	if _, err := refresher.Reconcile(t.Context(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := cache.List(testConfig, regionA); err != nil {
		t.Errorf("List of the reconciled provider config = %v, want its snapshot", err)
	}
	if _, err := cache.List("other", regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List of an untouched provider config = %v, want %v", err, ErrUnavailable)
	}
}

func TestReconcileEvictsAProviderConfigThatIsGone(t *testing.T) {
	cache := NewCache()
	refresher := &Refresher{
		Client: apiClient(),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  cache,
	}
	cache.store(testConfig, []provider.InstanceType{instanceType("small", regionA)})

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: testConfig}}
	if _, err := refresher.Reconcile(t.Context(), req); err != nil {
		t.Fatalf("Reconcile of a deleted provider config = %v, want no error", err)
	}

	if _, err := cache.List(testConfig, regionA); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List of a deleted provider config = %v, want %v", err, ErrUnavailable)
	}
	if _, ok := cache.Age(testConfig); ok {
		t.Error("Age must report no snapshot once the provider config is gone")
	}
}

func TestStartFillsTheCacheImmediatelyAndAgainOnEveryTick(t *testing.T) {
	const tick = 5 * time.Millisecond

	cache := NewCache()
	refresher := &Refresher{
		Client:   apiClient(providerConfig(testConfig)),
		Lister:   staticFactory(seededProvider(regionA)),
		Cache:    cache,
		Interval: tick,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- refresher.Start(ctx) }()

	waitFor(t, func() bool { return refresher.Counts().Success >= 1 }, "the cache to fill on start")
	waitFor(t, func() bool { return refresher.Counts().Success >= 3 }, "the ticker to refresh again")

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Start = %v, want no error on cancellation", err)
	}
}

func TestAnUnsetIntervalRefreshesHourly(t *testing.T) {
	if got := (&Refresher{}).interval(); got != RefreshInterval {
		t.Errorf("interval = %s, want %s", got, RefreshInterval)
	}
}

func TestTheRefresherRunsOnEveryReplica(t *testing.T) {
	if (&Refresher{}).NeedLeaderElection() {
		t.Error("the catalogue must be served by replicas that hold no lease")
	}
}

func waitFor(t *testing.T, done func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
