package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/metrics"
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
	testJoinToken       = "hcloud-join-token"
	namespaceFileName   = "namespace"
	namespaceFilePerm   = 0o600
	poolLabelAssignment = provider.PoolLabelAssignment

	providerRequestMetric = "horizon_provider_request_duration_seconds"
)

var testWatchdog = testPolicy(testRenewInterval, testSlack)

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
		operatorSecret("hcloud-join", "token", testJoinToken),
	}
}

func renderTemplate(t *testing.T, spec *v1alpha1.HetznerProviderSpec, template string) (string, error) {
	t.Helper()
	resolved, err := resolveSecretRefs(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, hetznerSecretRefs(spec))
	if err != nil {
		return "", err
	}
	resolved[cloudInitField] = template
	return renderCloudInit(spec, testWatchdog, resolved)
}

func secretRefPtr(name, key string) *corev1.SecretKeySelector {
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
		{
			name: "cloud-init needs a node token the spec does not configure",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CloudInitSecretRef = secretKeyRef(sentinelInitName, "user-data")
			}),
			wantErr: "cloud-init needs " + hetzner.NodeTokenSentinel + " but spec.hetzner sets no nodeCredentialSecretRef",
		},
		{
			name: "an unreadable secret is reported before a sentinel with no supplier",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.CloudInitSecretRef = secretKeyRef(sentinelInitName, "user-data")
				s.JoinTokenSecretRef = secretRefPtr("absent", "token")
			}),
			wantErr: `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prov, err := hetznerProvider(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, tc.spec, testWatchdog)
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
				s.NodeCredentialSecretRef = secretRefPtr("hcloud-node", "token")
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
				s.NodeCredentialSecretRef = secretRefPtr("absent", "token")
			}),
			template: sentinelCloudInit,
			wantErr:  `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
		{
			name: "the node credential key is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.NodeCredentialSecretRef = secretRefPtr("hcloud-node", "absent")
			}),
			template: sentinelCloudInit,
			wantErr:  `secret ` + testOperatorNS + `/hcloud-node has no key "absent"`,
		},
		{
			name:     "the token sentinel is used without a node credential",
			spec:     hetznerSpec(nil),
			template: sentinelCloudInit,
			wantErr:  "cloud-init needs " + hetzner.NodeTokenSentinel + " but spec.hetzner sets no nodeCredentialSecretRef",
		},
		{
			name:     "the max lifetime always resolves from the watchdog policy",
			spec:     hetznerSpec(nil),
			template: "#cloud-config\nnode-label: " + poolLabelAssignment + "\nmax-lifetime: " + hetzner.MaxLifetimeSentinel + "\n",
			want:     "#cloud-config\nnode-label: " + poolLabelAssignment + "\nmax-lifetime: " + testWatchdog.MaxLifetime.Duration.String() + "\n",
		},
		{
			name: "the join token reaches the cloud-init when configured",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.JoinTokenSecretRef = secretRefPtr("hcloud-join", "token")
			}),
			template: "#cloud-config\nnode-label: " + poolLabelAssignment + "\njoin-token: " + hetzner.JoinTokenSentinel + "\n",
			want:     "#cloud-config\nnode-label: " + poolLabelAssignment + "\njoin-token: " + testJoinToken + "\n",
		},
		{
			name: "the join token secret is absent",
			spec: hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
				s.JoinTokenSecretRef = secretRefPtr("absent", "token")
			}),
			template: "#cloud-config\nnode-label: " + poolLabelAssignment + "\njoin-token: " + hetzner.JoinTokenSentinel + "\n",
			wantErr:  `read secret ` + testOperatorNS + `/absent: secrets "absent" not found`,
		},
		{
			name:     "the join token sentinel is refused without a join token secret",
			spec:     hetznerSpec(nil),
			template: "#cloud-config\nnode-label: " + poolLabelAssignment + "\njoin-token: " + hetzner.JoinTokenSentinel + "\n",
			wantErr:  "cloud-init needs " + hetzner.JoinTokenSentinel + " but spec.hetzner sets no joinTokenSecretRef",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderTemplate(t, tc.spec, tc.template)
			assertErrorMessage(t, err, tc.wantErr)
			if got != tc.want {
				t.Errorf("rendered cloud-init is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderCloudInitSuppliesEverySentinelInTheVocabulary(t *testing.T) {
	spec := hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
		s.NodeCredentialSecretRef = secretRefPtr("hcloud-node", "token")
		s.JoinTokenSecretRef = secretRefPtr("hcloud-join", "token")
	})

	template := "#cloud-config\nnode-label: " + poolLabelAssignment + "\n"
	for i, sentinel := range provider.Sentinels() {
		template += "field-" + strconv.Itoa(i) + ": " + sentinel + "\n"
	}

	if _, err := renderTemplate(t, spec, template); err != nil {
		t.Fatalf("a fully configured specification left a sentinel without a supplier: %v", err)
	}
}

func TestHetznerProviderValidatesTheRenderedCloudInit(t *testing.T) {
	spec := hetznerSpec(func(s *v1alpha1.HetznerProviderSpec) {
		s.CloudInitSecretRef = secretKeyRef(sentinelInitName, "user-data")
		s.NodeCredentialSecretRef = secretRefPtr("hcloud-node", "token")
	})

	prov, err := hetznerProvider(t.Context(), newKubeClient(credentialSecrets()...), testOperatorNS, spec, testWatchdog)
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
			nodeCredential: secretRefPtr("hcloud-node", "token"),
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
			assertErrorMessage(t, requireTeardownGuarantee(cfg, prov.Capabilities()), tc.wantErr)
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

func catalogueSpec() *v1alpha1.HetznerProviderSpec {
	return hetznerSpec(func(spec *v1alpha1.HetznerProviderSpec) {
		spec.CloudInitSecretRef = secretKeyRef(sentinelInitName, "user-data")
		spec.NodeCredentialSecretRef = nil
	})
}

func TestNewCatalogueFactoryNeedsNothingBeyondTheToken(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	cfg := &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hetzner"},
		Spec:       v1alpha1.ProviderConfigSpec{Type: v1alpha1.ProviderTypeHetzner, Hetzner: catalogueSpec()},
	}

	provisioning, err := NewProviderFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the provider factory failed: %v", err)
	}
	if _, err := provisioning(t.Context(), cfg); err == nil {
		t.Fatal("the fixture no longer breaks cloud-init rendering, so it proves nothing")
	}

	factory, err := NewCatalogueFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the catalogue factory failed: %v", err)
	}
	lister, err := factory(t.Context(), cfg)
	assertErrorMessage(t, err, "")
	if lister == nil {
		t.Fatal("no lister returned, so the catalogue could never be filled")
	}
}

func TestNewCatalogueFactoryReportsAMissingToken(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewCatalogueFactory(newKubeClient())
	if err != nil {
		t.Fatalf("building the catalogue factory failed: %v", err)
	}

	_, err = factory(t.Context(), &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Type: v1alpha1.ProviderTypeHetzner, Hetzner: hetznerSpec(nil)},
	})
	assertErrorMessage(t, err, `read secret horizon-system/hcloud: secrets "hcloud" not found`)
}

func TestNewCatalogueFactoryRejectsAnUnknownProviderType(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewCatalogueFactory(newKubeClient())
	if err != nil {
		t.Fatalf("building the catalogue factory failed: %v", err)
	}

	_, err = factory(t.Context(), &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Type: "aws"},
	})
	assertErrorMessage(t, err, `unsupported provider type "aws"`)
}

func TestNewCatalogueFactoryRefusesToBuildWithoutANamespace(t *testing.T) {
	t.Setenv(podNamespaceEnvVar, "")
	setServiceAccountNSPath(t, filepath.Join(t.TempDir(), namespaceFileName))

	factory, err := NewCatalogueFactory(newKubeClient(credentialSecrets()...))
	if err == nil {
		t.Fatal("a factory was built without a resolvable namespace")
	}
	if factory != nil {
		t.Error("a factory was returned alongside an error")
	}
}

func meteredConfig(name string, spec *v1alpha1.HetznerProviderSpec) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ProviderConfigSpec{Type: v1alpha1.ProviderTypeHetzner, Hetzner: spec},
	}
}

// an empty region is refused before the client reaches the network, so the metric is all this proves
func assertTheClientIsMetered(t *testing.T, config string, list func(ctx context.Context, region string) ([]provider.InstanceType, error)) {
	t.Helper()
	baseline := snapshotSeries(t)

	if _, err := list(t.Context(), ""); err == nil {
		t.Fatal("listing instance types without a region must fail")
	}

	labels := map[string]string{
		"provider":  config,
		"operation": string(metrics.OperationListInstanceTypes),
		"result":    string(metrics.ResultFailure),
	}
	if count, _ := baseline.observations(t, providerRequestMetric, labels); count != 1 {
		t.Errorf("%s%v holds %d observations, want 1", providerRequestMetric, labels, count)
	}
}

func TestNewProviderFactoryMetersTheClientItBuilds(t *testing.T) {
	const config = "metered-provisioning"
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewProviderFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the factory failed: %v", err)
	}

	prov, err := factory(t.Context(), meteredConfig(config, hetznerSpec(nil)))
	if err != nil {
		t.Fatalf("building the provider failed: %v", err)
	}

	assertTheClientIsMetered(t, config, prov.ListInstanceTypes)
}

func TestNewCatalogueFactoryMetersTheClientItBuilds(t *testing.T) {
	const config = "metered-catalogue"
	t.Setenv(podNamespaceEnvVar, testOperatorNS)
	factory, err := NewCatalogueFactory(newKubeClient(credentialSecrets()...))
	if err != nil {
		t.Fatalf("building the catalogue factory failed: %v", err)
	}

	lister, err := factory(t.Context(), meteredConfig(config, catalogueSpec()))
	if err != nil {
		t.Fatalf("building the lister failed: %v", err)
	}

	assertTheClientIsMetered(t, config, lister.ListInstanceTypes)
}
