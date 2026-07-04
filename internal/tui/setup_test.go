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
	if cfg.Theme != config.ThemeDark {
		t.Errorf("Theme = %q, want %q", cfg.Theme, config.ThemeDark)
	}
}

func TestBuildSetupConfigReserved(t *testing.T) {
	in := setupInput{
		context:              "lab",
		cluster:              "burst",
		poolsNamespace:       "caph-system",
		poolTypesRaw:         "reserved=reserved-workers",
		theme:                config.ThemeDark,
		reservedTokenRaw:     "env=HCLOUD_TOKEN",
		reservedCloudInitRaw: "path=/etc/cloud-init.yaml",
		reservedLocation:     "hel1",
		reservedServerType:   "cpx22",
		reservedImageLabel:   "os",
		reservedImageValue:   "ubuntu-24.04",
		reservedSSHKeysRaw:   "primary, backup",
	}
	cfg, err := buildSetupConfig(in)
	if err != nil {
		t.Fatalf("buildSetupConfig: %v", err)
	}
	want := config.Reserved{
		Token:      config.CredentialSource{Env: "HCLOUD_TOKEN"},
		CloudInit:  config.CredentialSource{Path: "/etc/cloud-init.yaml"},
		Location:   "hel1",
		ServerType: "cpx22",
		Image:      config.ReservedImage{Label: "os", Value: "ubuntu-24.04"},
		SSHKeys:    []string{"primary", "backup"},
	}
	if !reflect.DeepEqual(cfg.Reserved, want) {
		t.Errorf("Reserved = %+v, want %+v", cfg.Reserved, want)
	}
}

func TestBuildSetupConfigInvalidCredential(t *testing.T) {
	in := setupInput{
		poolTypesRaw:     "reserved=reserved-workers",
		theme:            config.ThemeAuto,
		reservedTokenRaw: "vault=secret",
	}
	if _, err := buildSetupConfig(in); err == nil {
		t.Fatal("expected error for unknown credential kind")
	}
}

func TestParseCredentialSource(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    config.CredentialSource
		wantErr bool
	}{
		{name: "blank", raw: "", want: config.CredentialSource{}},
		{name: "value", raw: "value=abc123", want: config.CredentialSource{Value: "abc123"}},
		{name: "path", raw: "path=/etc/token", want: config.CredentialSource{Path: "/etc/token"}},
		{name: "env", raw: "env=HCLOUD_TOKEN", want: config.CredentialSource{Env: "HCLOUD_TOKEN"}},
		{
			name: "secret",
			raw:  "secret=caph-system/hcloud/token",
			want: config.CredentialSource{Secret: &config.SecretRef{Namespace: "caph-system", Name: "hcloud", Key: "token"}},
		},
		{name: "whitespace tolerant", raw: "  env = HCLOUD_TOKEN  ", want: config.CredentialSource{Env: "HCLOUD_TOKEN"}},
		{name: "no equals", raw: "value", wantErr: true},
		{name: "empty value", raw: "value=", wantErr: true},
		{name: "unknown kind", raw: "vault=secret", wantErr: true},
		{name: "malformed secret", raw: "secret=caph-system/hcloud", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCredentialSource(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCredentialSource(%q): %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCredentialSource(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
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
