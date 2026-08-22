package metrics

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var recordInstant = time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)

func TestRecordInstanceReleasedMovesTheWholeCostTrio(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "cost"), "hel1", "cx23"

	RecordInstanceReleased(config, region, instanceType, PathController, recordInstant, recordInstant.Add(10*time.Minute))
	RecordInstanceReleased(config, region, instanceType, PathController, recordInstant, recordInstant.Add(10*time.Minute))

	released := testutil.ToFloat64(instanceReleasedTotal.WithLabelValues(config, region, instanceType, string(PathController)))
	if released != 2 {
		t.Errorf("released count is %v, want 2", released)
	}

	seconds := testutil.ToFloat64(instanceSecondsTotal.WithLabelValues(config, region, instanceType))
	if seconds != 1200 {
		t.Errorf("instance seconds is %v, want 1200", seconds)
	}

	hours := testutil.ToFloat64(instanceBilledHoursTotal.WithLabelValues(config, region, instanceType))
	if hours != 2 {
		t.Errorf("billed hours is %v, want 2 because each ten-minute instance bills a whole hour", hours)
	}
}

func TestBilledHoursRoundsUpToTheBillingIncrement(t *testing.T) {
	cases := []struct {
		lifetime time.Duration
		want     float64
	}{
		{0, 0},
		{time.Second, 1},
		{59 * time.Minute, 1},
		{time.Hour, 1},
		{time.Hour + time.Second, 2},
		{150 * time.Minute, 3},
	}

	for _, tc := range cases {
		if got := billedHours(tc.lifetime); got != tc.want {
			t.Errorf("a lifetime of %s bills %v hours, want %v", tc.lifetime, got, tc.want)
		}
	}
}

func TestRecordInstanceReleasedIgnoresANegativeLifetime(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "skewed"), "hel1", "cx23"

	RecordInstanceReleased(config, region, instanceType, PathNode, recordInstant, recordInstant.Add(-time.Hour))

	if seconds := testutil.ToFloat64(instanceSecondsTotal.WithLabelValues(config, region, instanceType)); seconds != 0 {
		t.Errorf("instance seconds is %v, want 0", seconds)
	}
	if hours := testutil.ToFloat64(instanceBilledHoursTotal.WithLabelValues(config, region, instanceType)); hours != 0 {
		t.Errorf("billed hours is %v, want 0", hours)
	}
}

func TestRecordInstanceReleasedCountsAReleaseWhoseCreationInstantIsUnknown(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "undated"), "hel1", "cx23"

	RecordInstanceReleased(config, region, instanceType, PathOrphan, time.Time{}, recordInstant)

	released := testutil.ToFloat64(instanceReleasedTotal.WithLabelValues(config, region, instanceType, string(PathOrphan)))
	if released != 1 {
		t.Errorf("released count is %v, want 1 because the release happened whatever the cost was", released)
	}
	unknown := testutil.ToFloat64(instanceLifetimeUnknownTotal.WithLabelValues(config, region, instanceType))
	if unknown != 1 {
		t.Errorf("unknown lifetime count is %v, want 1 so the missing cost is visible rather than silent", unknown)
	}
	if seconds := testutil.ToFloat64(instanceSecondsTotal.WithLabelValues(config, region, instanceType)); seconds != 0 {
		t.Errorf("instance seconds is %v, want 0 because a zero cost would deflate the billed to theoretical ratio", seconds)
	}
	if hours := testutil.ToFloat64(instanceBilledHoursTotal.WithLabelValues(config, region, instanceType)); hours != 0 {
		t.Errorf("billed hours is %v, want 0 because a zero cost would deflate the billed to theoretical ratio", hours)
	}
}

func TestRecordInstanceReleasedLeavesTheUnknownCounterAloneForADatedRelease(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "dated"), "hel1", "cx23"

	RecordInstanceReleased(config, region, instanceType, PathNode, recordInstant, recordInstant.Add(time.Minute))

	if unknown := testutil.ToFloat64(instanceLifetimeUnknownTotal.WithLabelValues(config, region, instanceType)); unknown != 0 {
		t.Errorf("unknown lifetime count is %v, want 0", unknown)
	}
}

func TestCatalogueAgeSeriesIsAbsentUntilTheCatalogueIsFilled(t *testing.T) {
	config := ownedByTest(t, "unfilled")

	if series := seriesFor(t, catalogueAgeSeconds, config); len(series) != 0 {
		t.Fatalf("a provider config that never filled owns %d age series, want 0", len(series))
	}

	SetCatalogueAge(config, 0)

	if age := soleSeriesFor(t, catalogueAgeSeconds, config).GetGauge().GetValue(); age != 0 {
		t.Errorf("a catalogue filled this instant reports an age of %v, want 0", age)
	}

	ForgetProviderCatalogue(config)

	if series := seriesFor(t, catalogueAgeSeconds, config); len(series) != 0 {
		t.Errorf("a forgotten provider config still owns %d age series, want 0", len(series))
	}
}

func TestForgetProviderCatalogueDropsGaugesAndKeepsCounters(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "evicted"), "hel1", "cx23"

	SetInstanceTypePrice(config, region, instanceType, 0.0074)
	SetInstanceTypeCapacity(config, instanceType, 2, 4<<30)
	RecordCatalogueRefresh(config, ResultSuccess)

	ForgetProviderCatalogue(config)

	if refreshes := testutil.ToFloat64(catalogueRefreshTotal.WithLabelValues(config, string(ResultSuccess))); refreshes != 1 {
		t.Errorf("the refresh counter reads %v after eviction, want 1 because a counter must never reset", refreshes)
	}
	for name, gauge := range map[string]prometheus.Collector{
		"price":  instanceTypePriceEstimate,
		"cores":  instanceTypeCPUCores,
		"memory": instanceTypeMemoryBytes,
	} {
		if series := seriesFor(t, gauge, config); len(series) != 0 {
			t.Errorf("the %s gauge holds %d series after eviction, want 0", name, len(series))
		}
	}
}

func TestObserveLeaseReadyLandsInTheBucketAboveTheObservedJoinMode(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "ready"), "hel1", "cx23"

	ObserveLeaseReady(config, region, instanceType, SelectionPinned, 71*time.Second)

	histogram := soleSeriesFor(t, leaseReadySeconds, config).GetHistogram()
	var cumulative []uint64
	for _, bucket := range histogram.GetBucket() {
		cumulative = append(cumulative, bucket.GetCumulativeCount())
	}

	want := []uint64{0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1}
	if !slices.Equal(cumulative, want) {
		t.Errorf("a 71 second join fills buckets %v, want %v", cumulative, want)
	}
	if sum := histogram.GetSampleSum(); sum != 71 {
		t.Errorf("the observed sum is %v, want 71", sum)
	}
}

func TestObserveLeaseReadySeparatesJoinsBelowThirtySeconds(t *testing.T) {
	config, region, instanceType := ownedByTest(t, "subthirty"), "hel1", "cx23"

	ObserveLeaseReady(config, region, instanceType, SelectionPinned, 18*time.Second)

	histogram := soleSeriesFor(t, leaseReadySeconds, config).GetHistogram()
	var cumulative []uint64
	for _, bucket := range histogram.GetBucket() {
		cumulative = append(cumulative, bucket.GetCumulativeCount())
	}

	want := []uint64{0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	if !slices.Equal(cumulative, want) {
		t.Errorf("an 18 second join fills buckets %v, want %v", cumulative, want)
	}
}
