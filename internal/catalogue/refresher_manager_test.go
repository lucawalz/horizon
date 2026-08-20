package catalogue

import (
	"context"
	"slices"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	leaderElectionID               = "horizon-catalogue-test"
	leaderElectionNamespace        = "default"
	otherReplica                   = "another-replica"
	managedInterval                = 20 * time.Millisecond
	refreshesTheWatchCannotExplain = 5
)

func TestTheControllerRefreshesProviderConfigEventsWithoutTheLease(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	holdTheLeaderLease(t)

	config := acceptedProviderConfig(testConfig, 8*time.Hour)
	createProviderConfig(t, config)

	prov := seededProvider(regionA)
	cache := NewCache()
	refresher := &Refresher{Lister: staticFactory(prov), Cache: cache}
	startManagerWith(t, refresher)

	waitFor(t, func() bool { return offers(cache, config.Name, "small") }, "the create event to fill the catalogue")

	prov.SeedInstanceType(instanceType("large", regionA))
	bumpGeneration(t, config)

	waitFor(t, func() bool { return offers(cache, config.Name, "large") }, "a generation change to refresh out of band")

	assertLeaseStillHeldElsewhere(t)
}

func TestTheManagerTicksTheRefresherWithoutTheLease(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	holdTheLeaderLease(t)
	createProviderConfig(t, acceptedProviderConfig(testConfig, 8*time.Hour))

	refresher := &Refresher{
		Lister:   staticFactory(seededProvider(regionA)),
		Cache:    NewCache(),
		Interval: managedInterval,
	}
	startManagerWith(t, refresher)

	waitFor(t, func() bool {
		return refresher.Counts().Success >= refreshesTheWatchCannotExplain
	}, "the refresher to keep refreshing while no provider config changes")

	assertLeaseStillHeldElsewhere(t)
}

func offers(cache *Cache, config, name string) bool {
	types, err := cache.List(config, regionA)
	if err != nil {
		return false
	}
	return slices.Contains(names(types), name)
}

func holdTheLeaderLease(t *testing.T) {
	t.Helper()
	held := metav1.NewMicroTime(time.Now())
	duration := int32(time.Hour / time.Second)
	transitions := int32(0)
	holder := otherReplica
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: leaderElectionID, Namespace: leaderElectionNamespace},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &duration,
			AcquireTime:          &held,
			RenewTime:            &held,
			LeaseTransitions:     &transitions,
		},
	}
	if err := testEnv.Client.Create(t.Context(), lease); err != nil {
		t.Fatalf("hold the leader lease: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Client.Delete(context.Background(), lease) })
}

func assertLeaseStillHeldElsewhere(t *testing.T) {
	t.Helper()
	var lease coordinationv1.Lease
	key := types.NamespacedName{Name: leaderElectionID, Namespace: leaderElectionNamespace}
	if err := testEnv.Client.Get(t.Context(), key, &lease); err != nil {
		t.Fatalf("read the leader lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != otherReplica {
		t.Fatalf("leader lease holder = %v, want the test to have run without leadership", lease.Spec.HolderIdentity)
	}
}

func createProviderConfig(t *testing.T, config *v1alpha1.ProviderConfig) {
	t.Helper()
	if err := testEnv.Client.Create(t.Context(), config); err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Client.Delete(context.Background(), config) })
}

func bumpGeneration(t *testing.T, config *v1alpha1.ProviderConfig) {
	t.Helper()
	config.Spec.Watchdog.MaxLifetime = metav1.Duration{Duration: 6 * time.Hour}
	if err := testEnv.Client.Update(t.Context(), config); err != nil {
		t.Fatalf("update provider config: %v", err)
	}
}

func startManagerWith(t *testing.T, refresher *Refresher) {
	t.Helper()
	reusedAcrossTests := true
	mgr, err := ctrl.NewManager(testEnv.Config, ctrl.Options{
		Scheme:                  clusterScheme(),
		Metrics:                 metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:  "0",
		LeaderElection:          true,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: leaderElectionNamespace,
		Controller:              ctrlconfig.Controller{SkipNameValidation: &reusedAcrossTests},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	refresher.Client = mgr.GetClient()
	if err := refresher.SetupWithManager(mgr); err != nil {
		t.Fatalf("set up the refresher: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-stopped; err != nil {
			t.Errorf("manager stopped with %v", err)
		}
	})
}
