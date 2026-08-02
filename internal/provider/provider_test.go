package provider_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
)

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
