package tui

import (
	"os"
	"path/filepath"
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

func makeRepoTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"bedrock", "bedfellow", "other", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bedrock-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCompleteRepoPath(t *testing.T) {
	root := makeRepoTree(t)
	sep := string(filepath.Separator)

	if got, cands := completeRepoPath(filepath.Join(root, "o")); got != filepath.Join(root, "other")+sep || cands != nil {
		t.Errorf("single match = (%q, %v), want %q with no candidates", got, cands, filepath.Join(root, "other")+sep)
	}

	got, cands := completeRepoPath(filepath.Join(root, "bed"))
	if got != filepath.Join(root, "bed") {
		t.Errorf("multi match completed = %q, want %q", got, filepath.Join(root, "bed"))
	}
	if !reflect.DeepEqual(cands, []string{"bedfellow", "bedrock"}) {
		t.Errorf("multi match candidates = %v, want [bedfellow bedrock] (files and dotdirs excluded)", cands)
	}

	if _, cands := completeRepoPath(root + sep); contains(cands, ".hidden") {
		t.Errorf("dotdir leaked without a dot base: %v", cands)
	}
	if got, _ := completeRepoPath(root + sep + "."); got != filepath.Join(root, ".hidden")+sep {
		t.Errorf("dot base completion = %q, want %q", got, filepath.Join(root, ".hidden")+sep)
	}

	if got, cands := completeRepoPath(""); got != "" || cands != nil {
		t.Errorf("empty input = (%q, %v), want empty", got, cands)
	}
	if got, cands := completeRepoPath(filepath.Join(root, "zzz")); got != filepath.Join(root, "zzz") || cands != nil {
		t.Errorf("no match = (%q, %v), want input unchanged", got, cands)
	}

	t.Setenv("HOME", root)
	if got, cands := completeRepoPath("~/bed"); got != "~/bed" || !reflect.DeepEqual(cands, []string{"bedfellow", "bedrock"}) {
		t.Errorf("tilde round-trip = (%q, %v), want (~/bed, [bedfellow bedrock])", got, cands)
	}
	if got, _ := completeRepoPath("~/o"); got != "~/other"+sep {
		t.Errorf("tilde single match = %q, want ~/other%s", got, sep)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestNormalizeRepoPath(t *testing.T) {
	root := makeRepoTree(t)
	t.Setenv("HOME", root)

	for _, in := range []string{"", "   ", "~", "~/", root} {
		if got, err := normalizeRepoPath(in); err != nil || got != "" {
			t.Errorf("normalizeRepoPath(%q) = (%q, %v), want empty skip", in, got, err)
		}
	}

	dir := filepath.Join(root, "bedrock")
	if got, err := normalizeRepoPath(dir); err != nil || got != dir {
		t.Errorf("existing dir = (%q, %v), want passthrough", got, err)
	}
	if got, err := normalizeRepoPath("~/bedrock"); err != nil || got != "~/bedrock" {
		t.Errorf("tilde dir = (%q, %v), want ~/bedrock preserved", got, err)
	}
	if _, err := normalizeRepoPath(filepath.Join(root, "nope")); err == nil {
		t.Error("nonexistent path should error")
	}
	if _, err := normalizeRepoPath(filepath.Join(root, "bedrock-file")); err == nil {
		t.Error("regular file should error (not a directory)")
	}
}
