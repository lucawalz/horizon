package tui

import (
	"reflect"
	"testing"

	"github.com/lucawalz/horizon/internal/config"
)

func TestBuildSetupConfigValid(t *testing.T) {
	in := setupInput{
		context:        "lab",
		cluster:        "burst",
		poolsNamespace: "caph-system",
		poolTypesRaw:   "elastic=elastic-workers,reserved=reserved-workers",
		repoPath:       "/tmp/repo",
		ccClass:        "hetzner",
		ccWorkerClass:  "hetzner-worker",
		theme:          config.ThemeDark,
	}
	cfg, err := buildSetupConfig(in)
	if err != nil {
		t.Fatalf("buildSetupConfig: %v", err)
	}
	if cfg.Context != "lab" {
		t.Errorf("Context = %q, want lab", cfg.Context)
	}
	if cfg.Cluster != "burst" {
		t.Errorf("Cluster = %q, want burst", cfg.Cluster)
	}
	if cfg.Pools.Namespace != "caph-system" {
		t.Errorf("Pools.Namespace = %q, want caph-system", cfg.Pools.Namespace)
	}
	wantTypes := map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"}
	if !reflect.DeepEqual(cfg.Pools.Types, wantTypes) {
		t.Errorf("Pools.Types = %v, want %v", cfg.Pools.Types, wantTypes)
	}
	if cfg.RepoPath != "/tmp/repo" {
		t.Errorf("RepoPath = %q, want /tmp/repo", cfg.RepoPath)
	}
	if cfg.ClusterCreate.Class != "hetzner" {
		t.Errorf("Class = %q, want hetzner", cfg.ClusterCreate.Class)
	}
	if cfg.ClusterCreate.WorkerClass != "hetzner-worker" {
		t.Errorf("WorkerClass = %q, want hetzner-worker", cfg.ClusterCreate.WorkerClass)
	}
	if cfg.Theme != config.ThemeDark {
		t.Errorf("Theme = %q, want %q", cfg.Theme, config.ThemeDark)
	}
}

func TestBuildSetupConfigInvalidTheme(t *testing.T) {
	in := setupInput{
		poolTypesRaw: "elastic=elastic-workers",
		theme:        "neon",
	}
	if _, err := buildSetupConfig(in); err == nil {
		t.Fatal("expected error for invalid theme")
	}
}

func TestBuildSetupConfigMalformedPoolTypes(t *testing.T) {
	in := setupInput{
		poolTypesRaw: "elastic",
		theme:        config.ThemeAuto,
	}
	if _, err := buildSetupConfig(in); err == nil {
		t.Fatal("expected error for malformed pool types")
	}
}

func TestParsePoolTypes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "two entries",
			raw:  "elastic=elastic-workers,reserved=reserved-workers",
			want: map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"},
		},
		{
			name: "whitespace tolerant",
			raw:  "  elastic = elastic-workers ,  reserved = reserved-workers  ",
			want: map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"},
		},
		{
			name: "trailing comma",
			raw:  "elastic=elastic-workers,",
			want: map[string]string{"elastic": "elastic-workers"},
		},
		{
			name:    "missing value",
			raw:     "elastic=",
			wantErr: true,
		},
		{
			name:    "no equals",
			raw:     "elastic",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePoolTypes(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePoolTypes(%q): %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePoolTypes(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
