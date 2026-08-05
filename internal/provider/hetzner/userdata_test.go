package hetzner

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/provider"
)

const reservedCloudInit = `#cloud-config
write_files:
  - path: /etc/rancher/k3s/config.yaml
    content: |
      server: https://10.20.0.10:6443
      token: K10secret::server:abc
      node-label:
      - horizon.dev/pool=reserved
`

const sentinelCloudInit = `#cloud-config
write_files:
  - path: /etc/horizon/token
    content: ` + NodeTokenSentinel + `
  - path: /etc/rancher/k3s/config.yaml
    content: |
      node-label:
      - horizon.dev/pool=reserved
runcmd:
  - curl -fsSLo /tmp/horizon.tar.gz https://example.invalid/horizon_` + VersionSentinel + `_linux_amd64.tar.gz
`

const (
	testNodeToken = "node-token-value"
	testVersion   = "1.2.3"
)

func fullValues() map[string]string {
	return map[string]string{
		NodeTokenSentinel: testNodeToken,
		VersionSentinel:   testVersion,
	}
}

func vocabularyTemplate() (string, map[string]string) {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	values := make(map[string]string, len(provider.Sentinels()))
	for i, sentinel := range provider.Sentinels() {
		value := "resolved-" + strconv.Itoa(i)
		b.WriteString("field-" + strconv.Itoa(i) + ": " + sentinel + "\n")
		values[sentinel] = value
	}
	return b.String(), values
}

func TestRenderUserDataResolvesEverySentinel(t *testing.T) {
	template, values := vocabularyTemplate()
	rendered, err := RenderUserData(template, values)
	if err != nil {
		t.Fatalf("RenderUserData: %v", err)
	}
	if strings.Contains(rendered, sentinelPrefix) {
		t.Errorf("rendered cloud-init still carries a sentinel:\n%s", rendered)
	}
	for sentinel, value := range values {
		if !strings.Contains(rendered, value) {
			t.Errorf("rendered cloud-init does not carry the value for %s", sentinel)
		}
	}
}

func TestRenderUserDataRejectsEverySentinelLeftWithoutAValue(t *testing.T) {
	for _, missing := range provider.Sentinels() {
		t.Run(missing, func(t *testing.T) {
			template, values := vocabularyTemplate()
			delete(values, missing)
			_, err := RenderUserData(template, values)
			if err == nil {
				t.Fatalf("%s resolved without a value", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error is %q, want it to name %s", err, missing)
			}
		})
	}
}

func TestRenderUserDataRejectsAnUnresolvedSentinel(t *testing.T) {
	tests := []struct {
		name     string
		template string
		values   map[string]string
		wantErr  string
	}{
		{
			name:     "no value supplied for a known sentinel",
			template: sentinelCloudInit,
			values:   map[string]string{VersionSentinel: testVersion},
			wantErr:  "hetzner: cloud-init leaves " + NodeTokenSentinel + " unresolved",
		},
		{
			name:     "sentinel nothing supplies",
			template: "#cloud-config\ntoken: ${HORIZON_MISSPELLED}\n",
			values:   fullValues(),
			wantErr:  "hetzner: cloud-init leaves ${HORIZON_MISSPELLED} unresolved",
		},
		{
			name:     "sentinel that is never closed",
			template: "#cloud-config\ntoken: ${HORIZON_NODE_TOKEN\n",
			values:   fullValues(),
			wantErr:  "hetzner: cloud-init leaves " + sentinelPrefix + " unresolved",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := RenderUserData(tc.template, tc.values)
			if err == nil {
				t.Fatalf("rendered %q without an error", rendered)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("error is %q, want %q", err.Error(), tc.wantErr)
			}
			if rendered != "" {
				t.Errorf("a rendered blob was returned alongside an error:\n%s", rendered)
			}
		})
	}
}

func TestRenderUserDataLeavesABlobWithoutSentinelsUntouched(t *testing.T) {
	tests := []struct {
		name     string
		template string
	}{
		{name: "plain cloud-init", template: reservedCloudInit},
		{
			name:     "braces and dollars that are not sentinels",
			template: "#cloud-config\nruncmd:\n  - echo ${HOME} $PATH {{ ds.meta_data.hostname }} ${HORIZON}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := RenderUserData(tc.template, fullValues())
			if err != nil {
				t.Fatalf("RenderUserData: %v", err)
			}
			if rendered != tc.template {
				t.Errorf("cloud-init was rewritten:\n%s", rendered)
			}
		})
	}
}

func TestBuildUserDataValidatesWhatRenderingProduced(t *testing.T) {
	rendered, err := RenderUserData("#cloud-config\nnode-label: "+NodeTokenSentinel+"\n",
		map[string]string{NodeTokenSentinel: "horizon.dev/pool=reserved"})
	if err != nil {
		t.Fatalf("RenderUserData: %v", err)
	}
	out, err := buildUserData(rendered)
	if err != nil {
		t.Fatalf("buildUserData rejected a blob whose label came from a substitution: %v", err)
	}
	if out != rendered {
		t.Errorf("buildUserData returned %q, want the rendered blob %q", out, rendered)
	}
}

func TestBuildUserDataRejectsARenderedBlobWithoutTheReservedLabel(t *testing.T) {
	rendered, err := RenderUserData("#cloud-config\ntoken: "+NodeTokenSentinel+"\n", fullValues())
	if err != nil {
		t.Fatalf("RenderUserData: %v", err)
	}
	if _, err := buildUserData(rendered); err == nil {
		t.Fatal("expected error when the rendered cloud-init carries no reserved node-label")
	}
}

func TestBuildUserDataReturnsValidTemplateUnchanged(t *testing.T) {
	out, err := buildUserData(reservedCloudInit)
	if err != nil {
		t.Fatalf("buildUserData: %v", err)
	}
	if out != reservedCloudInit {
		t.Errorf("cloud-init must be returned unchanged:\n%s", out)
	}
}

func TestBuildUserDataFailsFastOnEmptyTemplate(t *testing.T) {
	if _, err := buildUserData("  \n"); err == nil {
		t.Fatal("expected error on empty cloud-init")
	}
}

func TestBuildUserDataFailsFastWhenReservedLabelMissing(t *testing.T) {
	if _, err := buildUserData("#cloud-config\nnode-label:\n- horizon.dev/pool=elastic\n"); err == nil {
		t.Fatal("expected error when reserved node-label is absent")
	}
}

func TestRenderUserDataSubstitutesMaxLifetime(t *testing.T) {
	out, err := RenderUserData("--max-lifetime="+MaxLifetimeSentinel, map[string]string{MaxLifetimeSentinel: "8h0m0s"})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if out != "--max-lifetime=8h0m0s" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderUserDataSubstitutesJoinToken(t *testing.T) {
	out, err := RenderUserData("token: "+JoinTokenSentinel, map[string]string{JoinTokenSentinel: "K10abc::server:secret"})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if out != "token: K10abc::server:secret" {
		t.Fatalf("got %q", out)
	}
}
