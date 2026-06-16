package tui

import (
	"strings"
	"testing"
)

func TestRenderBannerContainsArtAndSubLine(t *testing.T) {
	out := renderBanner(0, "edge", "prod")
	if strings.TrimSpace(out) == "" {
		t.Fatal("renderBanner returned empty output")
	}
	if !strings.Contains(out, "cluster command centre") {
		t.Errorf("banner missing sub-line:\n%s", out)
	}
	if !strings.Contains(out, "edge") || !strings.Contains(out, "prod") {
		t.Errorf("banner sub-line missing cluster/context:\n%s", out)
	}
	if lines := strings.Split(out, "\n"); len(lines) < len(strings.Split(bannerArt, "\n"))+1 {
		t.Errorf("banner missing art rows or sub-line, got %d lines", len(lines))
	}
}

func TestRenderBannerFallsBackToDefaults(t *testing.T) {
	out := renderBanner(0, "", "")
	if !strings.Contains(out, "default") || !strings.Contains(out, "current") {
		t.Errorf("banner should fall back to default/current:\n%s", out)
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("  ", "fallback"); got != "fallback" {
		t.Errorf("blank value = %q, want fallback", got)
	}
	if got := valueOr("set", "fallback"); got != "set" {
		t.Errorf("non-blank value = %q, want set", got)
	}
}
