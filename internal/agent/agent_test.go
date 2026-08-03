package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

const testInstanceID = "4711"

func testIdentity() Identity {
	return Identity{Name: testNodeName, InstanceID: testInstanceID}
}

func providerHolding(labels map[string]string) *fake.Provider {
	prov := fake.New()
	prov.Seed(provider.Instance{
		Name:       testNodeName,
		ProviderID: hetzner.ProviderIDPrefix + testInstanceID,
		Labels:     labels,
	})
	return prov
}

func expiryLabel(deadline time.Time) map[string]string {
	return map[string]string{provider.ExpiresAtLabelKey: provider.FormatExpiry(deadline)}
}

func TestProveIdentityHandsBackTheInstanceItAlreadyRead(t *testing.T) {
	expiry := armedDeadline()
	prov := providerHolding(expiryLabel(expiry))

	inst, err := proveIdentity(t.Context(), prov, testIdentity())
	if err != nil {
		t.Fatalf("prove the identity of a matching instance: %v", err)
	}
	if got := inst.Labels[provider.ExpiresAtLabelKey]; got != provider.FormatExpiry(expiry) {
		t.Errorf("expiry label = %q, want %q", got, provider.FormatExpiry(expiry))
	}
	if calls := prov.GetCalls(); len(calls) != 1 {
		t.Errorf("provider reads = %d, want the seed to reuse the single identity read", len(calls))
	}
}

func TestProveIdentityRejectsAProviderIDMismatch(t *testing.T) {
	prov := fake.New()
	prov.Seed(provider.Instance{Name: testNodeName, ProviderID: hetzner.ProviderIDPrefix + "9999"})

	if _, err := proveIdentity(t.Context(), prov, testIdentity()); err == nil {
		t.Fatal("a provider id mismatch armed the watchdog")
	}
}

func TestProveIdentityRejectsAnUnreadableProvider(t *testing.T) {
	prov := providerHolding(nil)
	prov.FailGet = func(string) error { return errors.New("the provider api is unreachable") }

	if _, err := proveIdentity(t.Context(), prov, testIdentity()); err == nil {
		t.Fatal("an unreadable provider armed the watchdog")
	}
}

func TestSeedWallDeadline(t *testing.T) {
	expiry := armedDeadline()

	tests := []struct {
		name   string
		labels map[string]string
		want   *time.Time
	}{
		{
			name:   "a well formed expiry label arms the wall clock",
			labels: expiryLabel(expiry),
			want:   &expiry,
		},
		{
			name: "an unlabelled instance leaves the backstop as the only clock",
		},
		{
			name:   "an instance without the expiry label leaves the backstop as the only clock",
			labels: map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue},
		},
		{
			name:   "an unreadable expiry label leaves the backstop as the only clock",
			labels: map[string]string{provider.ExpiresAtLabelKey: "tomorrow"},
		},
		{
			name:   "an empty expiry label leaves the backstop as the only clock",
			labels: map[string]string{provider.ExpiresAtLabelKey: ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := seedWallDeadline(t.Context(), provider.Instance{Name: testNodeName, Labels: tc.labels})
			switch {
			case tc.want == nil:
				if got != nil {
					t.Fatalf("seed = %s, want none", got)
				}
			case got == nil:
				t.Fatal("a readable expiry label yielded no seed")
			case !got.Equal(*tc.want):
				t.Errorf("seed = %s, want %s", got, *tc.want)
			}
		})
	}
}

func TestSeedWallDeadlineAlreadyPassedFiresOnTheFirstTick(t *testing.T) {
	expired := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)

	seed := seedWallDeadline(t.Context(), provider.Instance{Name: testNodeName, Labels: expiryLabel(expired)})
	if seed == nil {
		t.Fatal("a lease that expired before boot yielded no seed")
	}

	startedAt := time.Now()
	reason, due := fired(startedAt, time.Hour, seed, startedAt)
	if !due {
		t.Fatal("a seed already in the past did not fire on the first tick")
	}
	if reason != reasonWallClockDeadline {
		t.Errorf("reason = %q, want %q", reason, reasonWallClockDeadline)
	}
}
