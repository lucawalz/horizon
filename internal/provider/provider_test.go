package provider_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

func TestConfirmAbsentReportsAnInstanceTheProviderNoLongerHas(t *testing.T) {
	absent, err := provider.ConfirmAbsent(context.Background(), fake.New(), "gone")
	if err != nil {
		t.Fatalf("ConfirmAbsent: %v", err)
	}
	if !absent {
		t.Error("ConfirmAbsent must report absence when the provider reports not found")
	}
}

func TestConfirmAbsentReportsAnInstanceTheProviderStillHas(t *testing.T) {
	prov := fake.New()
	prov.Seed(provider.Instance{Name: "alive"})

	absent, err := provider.ConfirmAbsent(context.Background(), prov, "alive")
	if err != nil {
		t.Fatalf("ConfirmAbsent: %v", err)
	}
	if absent {
		t.Error("ConfirmAbsent must not report absence while the provider still returns the instance")
	}
}

func TestConfirmAbsentWrapsAnUnreadableProvider(t *testing.T) {
	unreachable := errors.New("provider is unreachable")
	prov := fake.New()
	prov.FailGet = func(string) error { return unreachable }

	absent, err := provider.ConfirmAbsent(context.Background(), prov, "unknown")
	if absent {
		t.Error("ConfirmAbsent must not report absence when the provider cannot be read")
	}
	if !errors.Is(err, unreachable) {
		t.Fatalf("ConfirmAbsent error = %v, want it to wrap %v", err, unreachable)
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("ConfirmAbsent error = %q, want it to name the instance", err)
	}
}

func TestFormatExpiryAvoidsCharactersProviderLabelsReject(t *testing.T) {
	got := provider.FormatExpiry(time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC))

	if strings.ContainsAny(got, ":+ ") {
		t.Errorf("expiry label value %q must stay within the label charset", got)
	}
}

func TestExpiryRoundTripsThroughALabel(t *testing.T) {
	deadline := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	labels := map[string]string{provider.ExpiresAtLabelKey: provider.FormatExpiry(deadline)}

	got, ok := provider.ParseExpiry(labels)
	if !ok {
		t.Fatalf("ParseExpiry(%v) reported no deadline", labels)
	}
	if !got.Equal(deadline) {
		t.Errorf("parsed deadline = %v, want %v", got, deadline)
	}
}

func TestParseExpiryReportsAbsentLabel(t *testing.T) {
	if _, ok := provider.ParseExpiry(map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue}); ok {
		t.Error("ParseExpiry must report absence when the deadline label is missing")
	}
}

func TestParseExpiryReportsUnreadableLabel(t *testing.T) {
	if _, ok := provider.ParseExpiry(map[string]string{provider.ExpiresAtLabelKey: "tomorrow"}); ok {
		t.Error("ParseExpiry must report absence when the deadline label cannot be read")
	}
}

func TestParseExpiryValueReadsABareTimestamp(t *testing.T) {
	deadline := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)

	got, ok := provider.ParseExpiryValue(provider.FormatExpiry(deadline))
	if !ok {
		t.Fatal("ParseExpiryValue reported no deadline for a formatted timestamp")
	}
	if !got.Equal(deadline) {
		t.Errorf("parsed deadline = %v, want %v", got, deadline)
	}
}

func TestParseExpiryValueRejectsWhatIsNotATimestamp(t *testing.T) {
	for _, raw := range []string{"", " ", "tomorrow", "1754136000.5", "0x10"} {
		if _, ok := provider.ParseExpiryValue(raw); ok {
			t.Errorf("ParseExpiryValue(%q) reported a deadline, want none", raw)
		}
	}
}
