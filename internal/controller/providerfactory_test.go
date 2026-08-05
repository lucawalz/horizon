package controller

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
	"github.com/lucawalz/horizon/internal/version"
)

const (
	testOperatorNS      = "horizon-system"
	testHetznerToken    = "hcloud-token"
	testImageName       = "ubuntu-24.04"
	blankTokenSecret    = "blank-token"
	unlabelledInitName  = "plain-cloud-init"
	sentinelInitName    = "watchdog-cloud-init"
	testNodeToken       = "hcloud-node-token"
	namespaceFileName   = "namespace"
	namespaceFilePerm   = 0o600
	poolLabelAssignment = provider.PoolLabelKey + "=" + provider.ReservedPoolValue
)

const sentinelCloudInit = "#cloud-config\nnode-label: " + poolLabelAssignment +
	"\ntoken: " + hetzner.NodeTokenSentinel +
	"\nrelease: " + hetzner.VersionSentinel + "\n"

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
		operatorSecret(sentinelInitName, "user-data", sentinelCloudInit),
		operatorSecret("hcloud-node", "token", testNodeToken),
	}
}

func nodeCredentialRef(name, key string) *corev1.SecretKeySelector {
	ref := secretKeyRef(name, key)
	return &ref
}

func hetznerSpec(mutate func(*v1alpha1.HetznerProviderSpec)) *v1alpha1.HetznerProviderSpec {
	spec := hetznerBlock()
	spec.ImageSelector = nil
	spec.Image = &v1alpha1.ImageSpec{Name: testImageName}
	if mutate != nil {
		mutate(spec)
	}
	return spec
}

func TestImageRef(t *testing.T) {
	cases := []struct {
		name string
		spec v1alpha1.HetznerProviderSpec
		want hetzner.ImageRef
	}{
		{
			name: "legacy selector maps onto the selector variant",
			spec: v1alpha1.HetznerProviderSpec{ImageSelector: map[string]string{"caph-image-name": "bedrock-cluster-node"}},
			want: hetzner.ImageRef{Selector: map[string]string{"caph-image-name": "bedrock-cluster-node"}},
		},
		{
			name: "image name",
			spec: v1alpha1.HetznerProviderSpec{Image: &v1alpha1.ImageSpec{Name: "ubuntu-24.04"}},
			want: hetzner.ImageRef{Name: "ubuntu-24.04"},
		},
		{
			name: "image id",
			spec: v1alpha1.HetznerProviderSpec{Image: &v1alpha1.ImageSpec{ID: 161547269}},
			want: hetzner.ImageRef{ID: 161547269},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := imageRef(&tc.spec)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestImageRefRejectsNeither(t *testing.T) {
	if _, err := imageRef(&v1alpha1.HetznerProviderSpec{}); err == nil {
		t.Fatal("expected an error when no image is configured")
	}
}

func TestImageRefRejectsBoth(t *testing.T) {
	spec := &v1alpha1.HetznerProviderSpec{
		Image:         &v1alpha1.ImageSpec{Name: "ubuntu-24.04"},
		ImageSelector: map[string]string{"caph-image-name": "bedrock-cluster-node"},
	}
	_, err := imageRef(spec)
	assertErrorMessage(t, err, "spec.hetzner sets both image and the deprecated imageSelector")
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
			name: "neither image nor the legacy selector is configured",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.Image = nil
			}),
			wantErr: "spec.hetzner needs either image or the deprecated imageSelector",
		},
		{
			name: "legacy image selector still resolves",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.Image = nil
				s.ImageSelector = map[string]string{"caph-image-name": "bedrock-cluster-node"}
			}),
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

func TestRenderCloudInitResolvesTheSentinelsACloudInitMayUse(t *testing.T) {
	tests := []struct {
		name     string
		spec     *v1alpha1.HetznerProviderSpec
		template string
		want     string
		wantErr  string
	}{
		{
			name: "node credential and version reach the cloud-init",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.NodeCredentialSecretRef = nodeCredentialRef("hcloud-node", "token")
			}),
			template: sentinelCloudInit,
			want: "#cloud-config\nnode-label: " + poolLabelAssignment +
				"\ntoken: " + testNodeToken + "\nrelease: " + version.Version() + "\n",
		},
		{
			name:     "a cloud-init without sentinels is untouched",
			spec:     hetznerSpec(nil),
			template: "#cloud-config\nnode-label: " + poolLabelAssignment + "\n",
			want:     "#cloud-config\nnode-label: " + poolLabelAssignment + "\n",
		},
		{
			name: "the node credential secret is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.NodeCredentialSecretRef = nodeCredentialRef("absent", "token")
			}),
			template: sentinelCloudInit,
			wantErr:  `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
		{
			name: "the node credential key is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.NodeCredentialSecretRef = nodeCredentialRef("hcloud-node", "absent")
			}),
			template: sentinelCloudInit,
			wantErr:  `secret ` + testOperatorNS + `/hcloud-node has no key "absent"`,
		},
		{
			name:     "the token sentinel is used without a node credential",
			spec:     hetznerSpec(nil),
			template: sentinelCloudInit,
			wantErr:  "hetzner: cloud-init leaves " + hetzner.NodeTokenSentinel + " unresolved",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderCloudInit(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, tc.spec, tc.template)
			assertErrorMessage(t, err, tc.wantErr)
			if got != tc.want {
				t.Errorf("rendered cloud-init is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHetznerProviderValidatesTheRenderedCloudInit(t *testing.T) {
	spec := hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
		s.CloudInitSecretRef = secretKeyRef(sentinelInitName, "user-data")
		s.NodeCredentialSecretRef = nodeCredentialRef("hcloud-node", "token")
	})

	prov, err := hetznerProvider(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, spec)
	assertErrorMessage(t, err, "")
	if prov == nil {
		t.Fatal("no provider returned for a cloud-init carrying sentinels")
	}
}

func TestRequireTeardownGuaranteeRefusesOnlyWhatCannotBeTornDown(t *testing.T) {
	tests := []struct {
		name           string
		selfTerminates bool
		nodeCredential *corev1.SecretKeySelector
		wantErr        string
	}{
		{
			name:           "self-terminating provider needs no node credential",
			selfTerminates: true,
		},
		{
			name:           "a node credential covers a provider that cannot self-terminate",
			nodeCredential: nodeCredentialRef("hcloud-node", "token"),
		},
		{
			name:    "neither the provider nor the configuration guarantees teardown",
			wantErr: `providerconfig "hetzner" cannot stop billing by self-terminating and configures no nodeCredentialSecretRef, so teardown of new capacity is not guaranteed`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prov := fake.New()
			prov.AdvertisedCapabilities.SelfTerminationStopsBilling = tc.selfTerminates
			cfg := &v1alpha1.ProviderConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "hetzner"},
				Spec: v1alpha1.ProviderConfigSpec{
					Type: v1alpha1.ProviderTypeHetzner,
					Hetzner: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
						s.NodeCredentialSecretRef = tc.nodeCredential
					}),
				},
			}
			assertErrorMessage(t, requireTeardownGuarantee(cfg, prov), tc.wantErr)
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

func TestNewProviderFactoryStillBuildsWithoutANodeCredential(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewProviderFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the factory failed: %v", err)
	}

	prov, err := factory(t.Context(), &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hetzner"},
		Spec:       v1alpha1.ProviderConfigSpec{Type: v1alpha1.ProviderTypeHetzner, Hetzner: hetznerSpec(nil)},
	})
	assertErrorMessage(t, err, "")
	if prov == nil {
		t.Fatal("no provider returned, so teardown and orphan collection would lose their client")
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
