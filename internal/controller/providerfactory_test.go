package controller

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	testOperatorNS      = "horizon-system"
	testHetznerToken    = "hcloud-token"
	testImageLabel      = "name"
	testImageValue      = "ubuntu-24.04"
	blankTokenSecret    = "blank-token"
	unlabelledInitName  = "plain-cloud-init"
	namespaceFileName   = "namespace"
	namespaceFilePerm   = 0o600
	poolLabelAssignment = provider.PoolLabelKey + "=" + provider.ReservedPoolValue
)

func assertErrorMessage(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Fatalf("no error returned, want %q", want)
	case want != "" && err.Error() != want:
		t.Fatalf("error is %q, want %q", err.Error(), want)
	}
}

func setServiceAccountNSPath(t *testing.T, path string) {
	t.Helper()
	original := serviceAccountNSPath
	t.Cleanup(func() { serviceAccountNSPath = original })
	serviceAccountNSPath = path
}

func writeServiceAccountNamespace(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), namespaceFileName)
	if err := os.WriteFile(path, []byte(content), namespaceFilePerm); err != nil {
		t.Fatalf("write namespace file: %v", err)
	}
	setServiceAccountNSPath(t, path)
	return path
}

func operatorSecret(name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testOperatorNS},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func credentialSecrets() []runtime.Object {
	return []runtime.Object{
		operatorSecret("hcloud", "token", testHetznerToken),
		operatorSecret(blankTokenSecret, "token", ""),
		operatorSecret("cloud-init", "user-data", "#cloud-config\nnode-label: "+poolLabelAssignment+"\n"),
		operatorSecret(unlabelledInitName, "user-data", "#cloud-config\n"),
	}
}

func hetznerSpec(mutate func(*v1alpha1.HetznerProviderSpec)) *v1alpha1.HetznerProviderSpec {
	spec := hetznerBlock()
	spec.ImageSelector = map[string]string{testImageLabel: testImageValue}
	if mutate != nil {
		mutate(spec)
	}
	return spec
}

func TestImageSelectorAcceptsExactlyOneLabel(t *testing.T) {
	tests := []struct {
		name      string
		selector  map[string]string
		wantLabel string
		wantValue string
		wantErr   string
	}{
		{
			name:    "unset selector",
			wantErr: "imageSelector must carry exactly one label, got 0",
		},
		{
			name:     "empty selector",
			selector: map[string]string{},
			wantErr:  "imageSelector must carry exactly one label, got 0",
		},
		{
			name:      "one label",
			selector:  map[string]string{testImageLabel: testImageValue},
			wantLabel: testImageLabel,
			wantValue: testImageValue,
		},
		{
			name:     "two labels",
			selector: map[string]string{testImageLabel: testImageValue, "architecture": "arm"},
			wantErr:  "imageSelector must carry exactly one label, got 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			label, value, err := imageSelector(tc.selector)
			assertErrorMessage(t, err, tc.wantErr)
			if label != tc.wantLabel || value != tc.wantValue {
				t.Errorf("selector resolved to %q=%q, want %q=%q", label, value, tc.wantLabel, tc.wantValue)
			}
		})
	}
}

func TestSecretValueNamesWhatIsMissing(t *testing.T) {
	tests := []struct {
		name    string
		ref     corev1.SecretKeySelector
		want    string
		wantErr string
	}{
		{
			name:    "reference without a name",
			ref:     secretKeyRef("", "token"),
			wantErr: "credentialsSecretRef needs both a name and a key",
		},
		{
			name:    "reference without a key",
			ref:     secretKeyRef("hcloud", ""),
			wantErr: "credentialsSecretRef needs both a name and a key",
		},
		{
			name:    "secret is absent",
			ref:     secretKeyRef("absent", "token"),
			wantErr: `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
		{
			name:    "key is absent from an existing secret",
			ref:     secretKeyRef("hcloud", "api-key"),
			wantErr: `secret ` + testOperatorNS + `/hcloud has no key "api-key"`,
		},
		{
			name: "key resolves",
			ref:  secretKeyRef("hcloud", "token"),
			want: testHetznerToken,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := secretValue(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, "credentialsSecretRef", tc.ref)
			assertErrorMessage(t, err, tc.wantErr)
			if got != tc.want {
				t.Errorf("secret value is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOperatorNamespacePrefersTheEnvironment(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	writeServiceAccountNamespace(t, "from-the-file")

	got, err := operatorNamespace()
	assertErrorMessage(t, err, "")
	if got != testOperatorNS {
		t.Errorf("namespace is %q, want %q", got, testOperatorNS)
	}
}

func TestOperatorNamespaceFallsBackToTheServiceAccountFile(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, "")
	writeServiceAccountNamespace(t, " "+testOperatorNS+"\n")

	got, err := operatorNamespace()
	assertErrorMessage(t, err, "")
	if got != testOperatorNS {
		t.Errorf("namespace is %q, want %q", got, testOperatorNS)
	}
}

func TestOperatorNamespaceRejectsABlankServiceAccountFile(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, "")
	path := writeServiceAccountNamespace(t, "\n  \n")

	_, err := operatorNamespace()
	assertErrorMessage(t, err, "resolve operator namespace, "+path+" is empty")
}

func TestOperatorNamespaceFailsWithoutAnySource(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, "")
	setServiceAccountNSPath(t, filepath.Join(t.TempDir(), namespaceFileName))

	got, err := operatorNamespace()
	if err == nil {
		t.Fatalf("namespace resolved to %q without an environment variable or a file", got)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v does not wrap the missing file", err)
	}
	if want := "resolve operator namespace, set " + podNamespaceEnvVar + ": "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error is %q, want it to start with %q", err.Error(), want)
	}
}

func TestHetznerProviderReportsEveryConstructionFailure(t *testing.T) {
	tests := []struct {
		name    string
		spec    *v1alpha1.HetznerProviderSpec
		wantErr string
	}{
		{
			name: "every input resolves",
			spec: hetznerSpec(nil),
		},
		{
			name:    "no hetzner block",
			wantErr: `provider type "hetzner" carries no hetzner block`,
		},
		{
			name: "credentials secret is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CredentialsSecretRef = secretKeyRef("absent", "token")
			}),
			wantErr: `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
		{
			name: "cloud-init key is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CloudInitSecretRef = secretKeyRef("cloud-init", "absent")
			}),
			wantErr: `secret ` + testOperatorNS + `/cloud-init has no key "absent"`,
		},
		{
			name: "image selector carries no label",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.ImageSelector = nil
			}),
			wantErr: "imageSelector must carry exactly one label, got 0",
		},
		{
			name: "token is blank",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CredentialsSecretRef = secretKeyRef(blankTokenSecret, "token")
			}),
			wantErr: "hetzner: token must not be empty",
		},
		{
			name: "cloud-init does not label the node",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CloudInitSecretRef = secretKeyRef(unlabelledInitName, "user-data")
			}),
			wantErr: `hetzner: cloud-init missing node-label "` + poolLabelAssignment + `"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prov, err := hetznerProvider(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, tc.spec)
			assertErrorMessage(t, err, tc.wantErr)
			if tc.wantErr == "" && prov == nil {
				t.Error("no provider returned for a fully resolved specification")
			}
			if tc.wantErr != "" && prov != nil {
				t.Error("a provider was returned alongside an error")
			}
		})
	}
}

func TestNewProviderFactoryBuildsTheConfiguredProvider(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewProviderFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the factory failed: %v", err)
	}

	prov, err := factory(t.Context(), &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Type: v1alpha1.ProviderTypeHetzner, Hetzner: hetznerSpec(nil)},
	})
	assertErrorMessage(t, err, "")
	if prov == nil {
		t.Fatal("no provider returned for a hetzner configuration")
	}
}

func TestNewProviderFactoryRejectsAnUnknownProviderType(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewProviderFactory(newKubeClient())
	if err != nil {
		t.Fatalf("building the factory failed: %v", err)
	}

	_, err = factory(t.Context(), &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Type: "aws"},
	})
	assertErrorMessage(t, err, `unsupported provider type "aws"`)
}

func TestNewProviderFactoryRefusesToBuildWithoutANamespace(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, "")
	setServiceAccountNSPath(t, filepath.Join(t.TempDir(), namespaceFileName))

	factory, err := NewProviderFactory(newKubeClient(credentialSecrets()...))
	if err == nil {
		t.Fatal("a factory was built without a resolvable namespace")
	}
	if factory != nil {
		t.Error("a factory was returned alongside an error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v does not wrap the namespace lookup failure", err)
	}
}
