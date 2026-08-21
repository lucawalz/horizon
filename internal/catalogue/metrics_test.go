package catalogue

import (
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/metrics"
	"github.com/lucawalz/horizon/internal/metrics/metricstest"
)

const (
	seededType   = "small"
	staleness    = 30 * time.Minute
	seededRate   = 0.006
	seededCores  = 2
	seededMemory = 4_000_000_000
)

func ownedByTest(t *testing.T, config string) string {
	t.Helper()
	t.Cleanup(func() { metrics.ForgetProviderCatalogue(config) })
	return config
}

func ownLabels(config string, extra map[string]string) map[string]string {
	labels := map[string]string{"provider": config}
	for key, value := range extra {
		labels[key] = value
	}
	return labels
}

func refreshesRecorded(t *testing.T, taken metricstest.Snapshot, config string, result metrics.Result) float64 {
	t.Helper()
	return taken.Counter(t, metricstest.CatalogueRefresh, ownLabels(config, map[string]string{"result": string(result)}))
}

func assertGauge(t *testing.T, name string, labels map[string]string, want float64) {
	t.Helper()
	got, published := metricstest.Gauge(t, name, labels)
	if !published {
		t.Fatalf("%s%v was never published", name, labels)
	}
	if got != want {
		t.Errorf("%s%v is %v, want %v", name, labels, got, want)
	}
}

func assertNoCatalogueGauges(t *testing.T, config string) {
	t.Helper()
	for _, name := range []string{
		metricstest.CatalogueAge,
		metricstest.InstanceTypePrice,
		metricstest.InstanceTypeCores,
		metricstest.InstanceTypeMemory,
	} {
		if series := metricstest.AllSeries(t, name, ownLabels(config, nil)); len(series) != 0 {
			t.Errorf("%s still holds %d series for %q, want none", name, len(series), config)
		}
	}
}

func TestASuccessfulRefreshIsCountedAndPublishesTheCatalogue(t *testing.T) {
	config := ownedByTest(t, "counted-success")
	taken := metricstest.Take(t)
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  NewCache(),
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}

	if got := refreshesRecorded(t, taken, config, metrics.ResultSuccess); got != 1 {
		t.Errorf("successful refreshes recorded = %v, want 1", got)
	}
	priced := ownLabels(config, map[string]string{"region": regionA, "instance_type": seededType})
	assertGauge(t, metricstest.InstanceTypePrice, priced, seededRate)
	sized := ownLabels(config, map[string]string{"instance_type": seededType})
	assertGauge(t, metricstest.InstanceTypeCores, sized, seededCores)
	assertGauge(t, metricstest.InstanceTypeMemory, sized, seededMemory)
}

func TestAFailedRefreshIsCountedAsAFailure(t *testing.T) {
	config := ownedByTest(t, "counted-failure")
	prov := seededProvider(regionA)
	prov.FailListInstanceTypes = func(string) error { return errors.New("hetzner is down") }
	taken := metricstest.Take(t)
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(prov),
		Cache:  NewCache(),
	}

	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	if got := refreshesRecorded(t, taken, config, metrics.ResultFailure); got != 1 {
		t.Errorf("failed refreshes recorded = %v, want 1", got)
	}
	if got := refreshesRecorded(t, taken, config, metrics.ResultSuccess); got != 0 {
		t.Errorf("successful refreshes recorded = %v, want 0", got)
	}
}

func TestTheAgeGaugeStaysAbsentUntilTheCatalogueFills(t *testing.T) {
	config := ownedByTest(t, "never-filled")
	prov := seededProvider(regionA)
	prov.FailListInstanceTypes = func(string) error { return errors.New("hetzner is down") }
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(prov),
		Cache:  NewCache(),
	}

	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	if series := metricstest.AllSeries(t, metricstest.CatalogueAge, ownLabels(config, nil)); len(series) != 0 {
		t.Errorf("a catalogue that never filled reports an age of %v, want no series at all", series)
	}
}

func TestTheAgeGaugeReportsHowStaleTheServedSnapshotIs(t *testing.T) {
	config := ownedByTest(t, "stale")
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	prov := seededProvider(regionA)
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(prov),
		Cache:  NewCacheWithClock(func() time.Time { return now }),
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}
	assertGauge(t, metricstest.CatalogueAge, ownLabels(config, nil), 0)

	now = now.Add(staleness)
	prov.FailListInstanceTypes = func(string) error { return errors.New("hetzner is down") }
	if err := refresher.refreshAll(t.Context()); err == nil {
		t.Fatal("refreshAll must report the provider failure")
	}

	assertGauge(t, metricstest.CatalogueAge, ownLabels(config, nil), staleness.Seconds())
}

func TestAProviderConfigThatIsGoneLosesItsGaugesAndKeepsItsCounter(t *testing.T) {
	config := ownedByTest(t, "swept-away")
	taken := metricstest.Take(t)
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  NewCache(),
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}

	refresher.Client = apiClient()
	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll after the provider config was deleted: %v", err)
	}

	assertNoCatalogueGauges(t, config)
	if got := refreshesRecorded(t, taken, config, metrics.ResultSuccess); got != 1 {
		t.Errorf("successful refreshes recorded = %v, want the counter kept at 1 so no rate sees a reset", got)
	}
}

func TestReconcilingADeletedProviderConfigForgetsItsGauges(t *testing.T) {
	config := ownedByTest(t, "reconciled-away")
	refresher := &Refresher{
		Client: apiClient(providerConfig(config)),
		Lister: staticFactory(seededProvider(regionA)),
		Cache:  NewCache(),
	}

	if err := refresher.refreshAll(t.Context()); err != nil {
		t.Fatalf("refreshAll: %v", err)
	}

	refresher.Client = apiClient()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: config}}
	if _, err := refresher.Reconcile(t.Context(), req); err != nil {
		t.Fatalf("Reconcile of a deleted provider config = %v, want no error", err)
	}

	assertNoCatalogueGauges(t, config)
}
