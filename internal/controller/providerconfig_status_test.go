package controller

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

const publisherNamespace = "horizon"

const usableCloudInit = "#cloud-config\nruncmd:\n  - k3s agent --node-label " + provider.PoolLabelAssignment + "\n"

var errCatalogueFetch = errors.New("the provider rejected the token")

func publisherSecrets() []runtime.Object {
	return []runtime.Object{
		secretWith("hcloud", "token", "a-token"),
		secretWith("cloud-init", "user-data", usableCloudInit),
		secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
	}
}

func secretWith(name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: publisherNamespace},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func newPublisher(api client.Client, secrets ...runtime.Object) *ProviderConfigPublisher {
	return newPublisherWithClock(api, newStubClock(), secrets...)
}

func newPublisherWithClock(api client.Client, clock *stubClock, secrets ...runtime.Object) *ProviderConfigPublisher {
	return &ProviderConfigPublisher{
		client:    api,
		kube:      k8sfake.NewSimpleClientset(secrets...),
		namespace: publisherNamespace,
		now:       clock.Now,
	}
}

func guaranteedProviderConfig(name string) *v1alpha1.ProviderConfig {
	config := validProviderConfig(name)
	ref := secretKeyRef("node-credential", "kubeconfig")
	config.Spec.Hetzner.NodeCredentialSecretRef = &ref
	return config
}

func fetchedType(name, region string) provider.InstanceType {
	return provider.InstanceType{
		Name:         name,
		Architecture: "x86",
		CPUType:      "shared",
		CPUCores:     2,
		MemoryBytes:  4 << 30,
		DiskBytes:    40 << 30,
		Region:       region,
		Available:    true,
		HourlyRate:   provider.Rate{Amount: 0.0074, Currency: "EUR"},
	}
}

func publishedConfig(t *testing.T, api client.Client, name string) *v1alpha1.ProviderConfig {
	t.Helper()
	var live v1alpha1.ProviderConfig
	if err := api.Get(t.Context(), client.ObjectKey{Name: name}, &live); err != nil {
		t.Fatalf("read the published provider config: %v", err)
	}
	return &live
}

func assertConditionDetail(t *testing.T, config *v1alpha1.ProviderConfig, name string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	found := meta.FindStatusCondition(config.Status.Conditions, name)
	if found == nil {
		t.Fatalf("condition %q is absent", name)
	}
	if found.Status != status || found.Reason != reason {
		t.Errorf("condition %q = %s/%s, want %s/%s", name, found.Status, found.Reason, status, reason)
	}
	if found.ObservedGeneration != config.Generation {
		t.Errorf("condition %q observed generation %d, want %d", name, found.ObservedGeneration, config.Generation)
	}
}

func fetchedTypes(count int) []provider.InstanceType {
	types := make([]provider.InstanceType, 0, count)
	for i := range count {
		types = append(types, fetchedType(fmt.Sprintf("cx%04d", i), "nbg1"))
	}
	return types
}

func TestPublishableCatalogueTruncatesOnlyBeyondTheCap(t *testing.T) {
	tests := []struct {
		name      string
		offered   int
		want      int
		truncated bool
	}{
		{"below the cap", v1alpha1.MaxPublishedInstanceTypes - 1, v1alpha1.MaxPublishedInstanceTypes - 1, false},
		{"at the cap", v1alpha1.MaxPublishedInstanceTypes, v1alpha1.MaxPublishedInstanceTypes, false},
		{"one beyond the cap", v1alpha1.MaxPublishedInstanceTypes + 1, v1alpha1.MaxPublishedInstanceTypes, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			published, truncated := publishableCatalogue(fetchedTypes(tc.offered))

			if len(published) != tc.want {
				t.Errorf("published %d of %d offered, want %d", len(published), tc.offered, tc.want)
			}
			if truncated != tc.truncated {
				t.Errorf("truncated = %t, want %t", truncated, tc.truncated)
			}
		})
	}
}

func TestPublishableCatalogueOrdersEntriesThatShareARegionAndName(t *testing.T) {
	small := fetchedType("cx22", "nbg1")
	large := fetchedType("cx22", "nbg1")
	large.MemoryBytes = 8 << 30

	forward, _ := publishableCatalogue([]provider.InstanceType{small, large})
	reverse, _ := publishableCatalogue([]provider.InstanceType{large, small})

	if !slices.Equal(forward, reverse) {
		t.Errorf("the same catalogue in a different order published %+v then %+v, want one order", forward, reverse)
	}
}

func TestPublishRecordsAReadyConfigAndItsCatalogue(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	fetched := []provider.InstanceType{fetchedType("cx32", "nbg1"), fetchedType("cx22", "nbg1"), fetchedType("cx22", "fsn1")}
	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	live := publishedConfig(t, api, config.Name)
	assertConditionDetail(t, live, v1alpha1.ConditionReady, metav1.ConditionTrue, reasonProviderConfigReady)
	assertConditionDetail(t, live, v1alpha1.ConditionCataloguePublished, metav1.ConditionTrue, reasonCataloguePublished)

	want := []v1alpha1.InstanceType{
		{
			Name: "cx22", Region: "fsn1", Architecture: "x86", CPUType: "shared", CPUCores: 2,
			MemoryBytes: 4 << 30, DiskBytes: 40 << 30, HourlyRate: "0.0074", Currency: "EUR", Available: true,
		},
		{
			Name: "cx22", Region: "nbg1", Architecture: "x86", CPUType: "shared", CPUCores: 2,
			MemoryBytes: 4 << 30, DiskBytes: 40 << 30, HourlyRate: "0.0074", Currency: "EUR", Available: true,
		},
		{
			Name: "cx32", Region: "nbg1", Architecture: "x86", CPUType: "shared", CPUCores: 2,
			MemoryBytes: 4 << 30, DiskBytes: 40 << 30, HourlyRate: "0.0074", Currency: "EUR", Available: true,
		},
	}
	if len(live.Status.InstanceTypes) != len(want) {
		t.Fatalf("published %d instance types, want %d", len(live.Status.InstanceTypes), len(want))
	}
	for i, published := range live.Status.InstanceTypes {
		if published != want[i] {
			t.Errorf("published[%d] = %+v, want %+v", i, published, want[i])
		}
	}
}

func TestReadinessNamesTheCheckThatFailed(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*v1alpha1.ProviderConfig)
		secrets  []runtime.Object
		fetchErr error
		status   metav1.ConditionStatus
		reason   string
	}{
		{
			name: "every check passes", mutate: func(*v1alpha1.ProviderConfig) {}, secrets: publisherSecrets(),
			status: metav1.ConditionTrue, reason: reasonProviderConfigReady,
		},
		{
			name: "credentials secret is missing", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{secretWith("cloud-init", "user-data", usableCloudInit)},
			status:  metav1.ConditionFalse, reason: reasonSecretUnresolved,
		},
		{
			name: "cloud-init secret has no such key", mutate: func(c *v1alpha1.ProviderConfig) {
				c.Spec.Hetzner.CloudInitSecretRef = secretKeyRef("cloud-init", "absent")
			},
			secrets: publisherSecrets(), status: metav1.ConditionFalse, reason: reasonSecretUnresolved,
		},
		{
			name: "teardown is not guaranteed", mutate: func(c *v1alpha1.ProviderConfig) {
				c.Spec.Hetzner.NodeCredentialSecretRef = nil
			},
			secrets: publisherSecrets(), status: metav1.ConditionFalse, reason: reasonTeardownNotGuaranteed,
		},
		{
			name: "cloud-init assigns no pool label", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{
				secretWith("hcloud", "token", "a-token"),
				secretWith("cloud-init", "user-data", "#cloud-config\nruncmd:\n  - k3s agent\n"),
				secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
			},
			status: metav1.ConditionFalse, reason: reasonProviderUnusable,
		},
		{
			name: "cloud-init is blank", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{
				secretWith("hcloud", "token", "a-token"),
				secretWith("cloud-init", "user-data", "   "),
				secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
			},
			status: metav1.ConditionFalse, reason: reasonProviderUnusable,
		},
		{
			name: "cloud-init leaves a sentinel unresolved", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{
				secretWith("hcloud", "token", "a-token"),
				secretWith("cloud-init", "user-data", usableCloudInit+"# ${HORIZON_UNKNOWN}\n"),
				secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
			},
			status: metav1.ConditionFalse, reason: reasonProviderUnusable,
		},
		{
			name: "cloud-init needs a token the spec does not configure", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{
				secretWith("hcloud", "token", "a-token"),
				secretWith("cloud-init", "user-data", usableCloudInit+"# "+hetzner.JoinTokenSentinel+"\n"),
				secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
			},
			status: metav1.ConditionFalse, reason: reasonProviderUnusable,
		},
		{
			name: "the api token resolves to an empty value", mutate: func(*v1alpha1.ProviderConfig) {},
			secrets: []runtime.Object{
				secretWith("hcloud", "token", ""),
				secretWith("cloud-init", "user-data", usableCloudInit),
				secretWith("node-credential", "kubeconfig", "a-kubeconfig"),
			},
			status: metav1.ConditionFalse, reason: reasonProviderUnusable,
		},
		{
			name: "the catalogue fetch failed", mutate: func(*v1alpha1.ProviderConfig) {}, secrets: publisherSecrets(),
			fetchErr: errCatalogueFetch, status: metav1.ConditionFalse, reason: reasonCatalogueUnavailable,
		},
		{
			name: "the provider offers nothing anywhere", mutate: func(*v1alpha1.ProviderConfig) {}, secrets: publisherSecrets(),
			fetchErr: catalogue.ErrEmpty, status: metav1.ConditionFalse, reason: reasonCatalogueEmpty,
		},
		{
			name: "the provider type has no implementation", mutate: func(c *v1alpha1.ProviderConfig) {
				c.Spec.Type = "aws"
			},
			secrets: publisherSecrets(), status: metav1.ConditionFalse, reason: reasonProviderUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := guaranteedProviderConfig(objectName(t))
			tc.mutate(config)

			found := newPublisher(nil, tc.secrets...).readiness(t.Context(), config, tc.fetchErr)

			if found.Status != tc.status || found.Reason != tc.reason {
				t.Errorf("Ready = %s/%s, want %s/%s", found.Status, found.Reason, tc.status, tc.reason)
			}
			if found.Message == "" {
				t.Error("the condition carries no message")
			}
		})
	}
}

func TestPublishWritesNothingWhenNothingChanged(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	fetched := []provider.InstanceType{fetchedType("cx22", "nbg1")}
	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	settled := publishedConfig(t, api, config.Name).ResourceVersion

	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("second Publish: %v", err)
	}

	if again := publishedConfig(t, api, config.Name).ResourceVersion; again != settled {
		t.Errorf("resourceVersion moved from %s to %s, want an unchanged status to write nothing", settled, again)
	}
}

func TestPublishKeepsTheLastCatalogueWhenTheFetchFails(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	if err := publisher.Publish(t.Context(), config, []provider.InstanceType{fetchedType("cx22", "nbg1")}, nil); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if err := publisher.Publish(t.Context(), config, nil, errCatalogueFetch); err != nil {
		t.Fatalf("second Publish: %v", err)
	}

	live := publishedConfig(t, api, config.Name)
	assertConditionDetail(t, live, v1alpha1.ConditionReady, metav1.ConditionFalse, reasonCatalogueUnavailable)
	assertConditionDetail(t, live, v1alpha1.ConditionCataloguePublished, metav1.ConditionFalse, reasonCatalogueUnavailable)
	if len(live.Status.InstanceTypes) != 1 {
		t.Errorf("published %d instance types, want the last good catalogue of 1", len(live.Status.InstanceTypes))
	}
}

func TestPublishKeepsTheLastCatalogueWhenTheFetchComesBackEmpty(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	if err := publisher.Publish(t.Context(), config, []provider.InstanceType{fetchedType("cx22", "nbg1")}, nil); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if err := publisher.Publish(t.Context(), config, nil, nil); err != nil {
		t.Fatalf("second Publish: %v", err)
	}

	live := publishedConfig(t, api, config.Name)
	if len(live.Status.InstanceTypes) != 1 {
		t.Errorf("published %d instance types, want the last good catalogue of 1", len(live.Status.InstanceTypes))
	}
	assertConditionDetail(t, live, v1alpha1.ConditionCataloguePublished, metav1.ConditionFalse, reasonCatalogueEmpty)
	assertConditionDetail(t, live, v1alpha1.ConditionReady, metav1.ConditionFalse, reasonCatalogueEmpty)
}

func TestPublishTruncatesACatalogueBeyondTheCap(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	if err := publisher.Publish(t.Context(), config, fetchedTypes(v1alpha1.MaxPublishedInstanceTypes+1), nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	live := publishedConfig(t, api, config.Name)
	if len(live.Status.InstanceTypes) != v1alpha1.MaxPublishedInstanceTypes {
		t.Errorf("published %d instance types, want the cap of %d", len(live.Status.InstanceTypes), v1alpha1.MaxPublishedInstanceTypes)
	}
	assertConditionDetail(t, live, v1alpha1.ConditionCataloguePublished, metav1.ConditionFalse, reasonCatalogueTruncated)
	assertConditionDetail(t, live, v1alpha1.ConditionReady, metav1.ConditionTrue, reasonProviderConfigReady)
}

func TestPublishStampsTheRefreshOncePerWindow(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	clock := newStubClock()
	publisher := newPublisherWithClock(api, clock, publisherSecrets()...)
	fetched := []provider.InstanceType{fetchedType("cx22", "nbg1")}

	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	stamped := publishedConfig(t, api, config.Name)
	if stamped.Status.CatalogueRefreshedAt == nil {
		t.Fatal("the first publish recorded no refresh time")
	}
	if !stamped.Status.CatalogueRefreshedAt.Time.Equal(clock.Now()) {
		t.Errorf("refresh time = %s, want %s", stamped.Status.CatalogueRefreshedAt.Time, clock.Now())
	}

	clock.Advance(catalogueStampInterval - time.Minute)
	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("Publish inside the window: %v", err)
	}
	inside := publishedConfig(t, api, config.Name)
	if inside.ResourceVersion != stamped.ResourceVersion {
		t.Errorf("resourceVersion moved from %s to %s, want a refresh inside the window to write nothing",
			stamped.ResourceVersion, inside.ResourceVersion)
	}

	clock.Advance(2 * time.Minute)
	if err := publisher.Publish(t.Context(), config, fetched, nil); err != nil {
		t.Fatalf("Publish beyond the window: %v", err)
	}
	beyond := publishedConfig(t, api, config.Name)
	if !beyond.Status.CatalogueRefreshedAt.Time.Equal(clock.Now()) {
		t.Errorf("refresh time = %s, want it to follow the clock to %s", beyond.Status.CatalogueRefreshedAt.Time, clock.Now())
	}
}

func TestPublishRecordsNoRefreshTimeWhenTheFetchFails(t *testing.T) {
	api := apiServerClient(t)
	config := guaranteedProviderConfig(objectName(t))
	assertCreate(t, api, config, false)
	publisher := newPublisher(api, publisherSecrets()...)

	if err := publisher.Publish(t.Context(), config, nil, errCatalogueFetch); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if stamp := publishedConfig(t, api, config.Name).Status.CatalogueRefreshedAt; stamp != nil {
		t.Errorf("refresh time = %s, want none for a fetch that never answered", stamp)
	}
}

func TestPublishIgnoresAProviderConfigThatIsGone(t *testing.T) {
	api := apiServerClient(t)
	publisher := newPublisher(api, publisherSecrets()...)

	err := publisher.Publish(t.Context(), guaranteedProviderConfig(objectName(t)), nil, nil)
	if err != nil {
		t.Errorf("Publish for a deleted provider config = %v, want no error", err)
	}
}
