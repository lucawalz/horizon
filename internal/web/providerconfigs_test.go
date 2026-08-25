package web

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const providerConfigsEndpoint = "/api/providerconfigs"

func configEndpoint(name string) string { return providerConfigsEndpoint + "/" + name }

func configSpecFixture() providerConfigSpecRequest {
	return providerConfigSpecRequest{
		Type: v1alpha1.ProviderTypeHetzner,
		Hetzner: &hetznerProviderRequest{
			CredentialsSecretRef:    secretKeyRequest{Name: "horizon-hetzner", Key: "token"},
			NodeCredentialSecretRef: &secretKeyRequest{Name: "horizon-hetzner-node", Key: "token"},
			JoinTokenSecretRef:      &secretKeyRequest{Name: "horizon-join-token", Key: "token"},
			CloudInitSecretRef:      secretKeyRequest{Name: "horizon-cloud-init", Key: "cloud-init"},
			Image:                   "ubuntu-24.04",
			SSHKeys:                 []string{"workstation"},
			Firewalls:               []string{"burst"},
		},
		Watchdog: watchdogRequest{
			RenewIntervalSeconds: seconds(time.Minute),
			SlackSeconds:         seconds(2 * time.Minute),
			MaxLifetimeSeconds:   seconds(8 * time.Hour),
		},
	}
}

func configRequestFixture(name string) providerConfigCreateRequest {
	return providerConfigCreateRequest{Name: name, providerConfigSpecRequest: configSpecFixture()}
}

func readConfig(t *testing.T, name string) *v1alpha1.ProviderConfig {
	t.Helper()
	var config v1alpha1.ProviderConfig
	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: name}, &config); err != nil {
		t.Fatalf("read provider config %s: %v", name, err)
	}
	return &config
}

func TestProviderConfigCreateStoresExactlyTheSubmittedFields(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "created-hetzner"
	removeConfigAfterTest(t, name)

	response := mutate(t, newWritingServer(t), http.MethodPost, providerConfigsEndpoint, configRequestFixture(name))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusCreated, response.Body)
	}
	if named := decodeBody[providerConfigSummary](t, response).Name; named != name {
		t.Errorf("the response names %q, want %q", named, name)
	}

	spec := readConfig(t, name).Spec
	if spec.Type != v1alpha1.ProviderTypeHetzner {
		t.Errorf("type = %q, want %q", spec.Type, v1alpha1.ProviderTypeHetzner)
	}

	hetzner := present(t, "hetzner", spec.Hetzner)
	if hetzner.CredentialsSecretRef.Name != "horizon-hetzner" || hetzner.CredentialsSecretRef.Key != "token" {
		t.Errorf("credentialsSecretRef = %+v, want the submitted name and key", hetzner.CredentialsSecretRef)
	}
	if hetzner.CloudInitSecretRef.Name != "horizon-cloud-init" || hetzner.CloudInitSecretRef.Key != "cloud-init" {
		t.Errorf("cloudInitSecretRef = %+v, want the submitted name and key", hetzner.CloudInitSecretRef)
	}
	if node := present(t, "nodeCredentialSecretRef", hetzner.NodeCredentialSecretRef); node.Name != "horizon-hetzner-node" {
		t.Errorf("nodeCredentialSecretRef names %q, want %q", node.Name, "horizon-hetzner-node")
	}
	if join := present(t, "joinTokenSecretRef", hetzner.JoinTokenSecretRef); join.Name != "horizon-join-token" {
		t.Errorf("joinTokenSecretRef names %q, want %q", join.Name, "horizon-join-token")
	}
	if image := present(t, "image", hetzner.Image); image.Name != "ubuntu-24.04" {
		t.Errorf("image names %q, want %q", image.Name, "ubuntu-24.04")
	}
	if len(hetzner.SSHKeys) != 1 || hetzner.SSHKeys[0] != "workstation" {
		t.Errorf("sshKeys = %v, want the submitted key alone", hetzner.SSHKeys)
	}
	if len(hetzner.Firewalls) != 1 || hetzner.Firewalls[0] != "burst" {
		t.Errorf("firewalls = %v, want the submitted firewall alone", hetzner.Firewalls)
	}

	watchdog := spec.Watchdog
	if watchdog.RenewInterval.Duration != time.Minute {
		t.Errorf("renewInterval = %s, want 1m", watchdog.RenewInterval.Duration)
	}
	if watchdog.Slack.Duration != 2*time.Minute {
		t.Errorf("slack = %s, want 2m", watchdog.Slack.Duration)
	}
	if watchdog.MaxLifetime.Duration != 8*time.Hour {
		t.Errorf("maxLifetime = %s, want 8h", watchdog.MaxLifetime.Duration)
	}
}

// the watchdog rules compare three fields against each other, so the apiserver names the one that was broken
func TestProviderConfigCreateSurfacesTheWatchdogRefusalVerbatim(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	for name, testCase := range map[string]struct {
		adjust func(*providerConfigCreateRequest)
		names  string
	}{
		"slack no greater than the renew interval": {
			adjust: func(r *providerConfigCreateRequest) { r.Watchdog.SlackSeconds = seconds(30 * time.Second) },
			names:  "slack must be greater than renewInterval",
		},
		"renew interval and slack past an hour": {
			adjust: func(r *providerConfigCreateRequest) {
				r.Watchdog.RenewIntervalSeconds = seconds(30 * time.Minute)
				r.Watchdog.SlackSeconds = seconds(40 * time.Minute)
			},
			names: "renewInterval plus slack must not exceed 1h",
		},
		"lifetime past a day": {
			adjust: func(r *providerConfigCreateRequest) { r.Watchdog.MaxLifetimeSeconds = seconds(25 * time.Hour) },
			names:  "maxLifetime must be between 5m and 24h",
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := "watchdog-" + strings.ReplaceAll(name, " ", "-")
			removeConfigAfterTest(t, config)

			request := configRequestFixture(config)
			testCase.adjust(&request)

			response := mutate(t, newWritingServer(t), http.MethodPost, providerConfigsEndpoint, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusUnprocessableEntity, response.Body)
			}
			if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, testCase.names) {
				t.Errorf("detail = %q, want the message the apiserver rejected it with", detail)
			}
		})
	}
}

func TestProviderConfigCreateRefusesASecondsCountNoDurationHolds(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	request := configRequestFixture("unreadable-watchdog")
	request.Watchdog.MaxLifetimeSeconds = maxDurationSeconds + 1

	response := mutate(t, newWritingServer(t), http.MethodPost, providerConfigsEndpoint, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusBadRequest, response.Body)
	}
}

func TestProviderConfigCreateRefusesABodyCarryingAnythingElse(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unknown-field-config"
	removeConfigAfterTest(t, name)

	body := map[string]any{"name": name, "type": v1alpha1.ProviderTypeHetzner, "credentials": "a token"}
	response := mutate(t, newWritingServer(t), http.MethodPost, providerConfigsEndpoint, body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusBadRequest, response.Body)
	}
	if err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: name}, &v1alpha1.ProviderConfig{}); !apierrors.IsNotFound(err) {
		t.Errorf("reading the refused config answered %v, want a not-found", err)
	}
}

func TestProviderConfigCreateRefusesEachCrossOriginFailureOnItsOwn(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newWritingServer(t)
	for name, spoil := range map[string]func(*http.Request){
		"rebound host": func(r *http.Request) {
			r.Host = "evil.example:8973"
			r.Header.Set(originHeader, "http://evil.example:8973")
		},
		"no interface header": func(r *http.Request) { r.Header.Del(interfaceHeader) },
		"no fetch metadata":   func(r *http.Request) { r.Header.Del(fetchSiteHeader) },
		"cross site fetch":    func(r *http.Request) { r.Header.Set(fetchSiteHeader, "cross-site") },
		"foreign origin":      func(r *http.Request) { r.Header.Set(originHeader, "http://evil.example") },
	} {
		t.Run(name, func(t *testing.T) {
			refused := "guarded-" + strings.ReplaceAll(name, " ", "-")
			removeConfigAfterTest(t, refused)

			request := newMutation(t, http.MethodPost, providerConfigsEndpoint, configRequestFixture(refused))
			spoil(request)

			if response := send(server, request); response.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d, body %s", response.Code, http.StatusForbidden, response.Body)
			}
			err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: refused}, &v1alpha1.ProviderConfig{})
			if !apierrors.IsNotFound(err) {
				t.Errorf("reading the refused config answered %v, want a not-found", err)
			}
		})
	}
}

// the two seams are held apart, so a process given one of them cannot be made to write through the other
func TestEachWriterSeamIsRefusedOnItsOwn(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	for name, testCase := range map[string]struct {
		options Options
		served  string
		refused string
		body    any
	}{
		"a lease writer alone": {
			options: Options{Client: testEnv.Client, Writer: LeaseWriterFor(testEnv.Client), Catalogue: AbsentCatalogue()},
			served:  leasesEndpoint,
			refused: providerConfigsEndpoint,
			body:    configRequestFixture("unwritable-config"),
		},
		"a provider config writer alone": {
			options: Options{
				Client:       testEnv.Client,
				ConfigWriter: ProviderConfigWriterFor(testEnv.Client),
				Catalogue:    AbsentCatalogue(),
			},
			served:  providerConfigsEndpoint,
			refused: leasesEndpoint,
			body:    createRequestFixture("unwritable-run"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			server, err := New(testCase.options)
			if err != nil {
				t.Fatalf("new server: %v", err)
			}
			anchor(t, server)

			response := mutate(t, server, http.MethodPost, testCase.refused, testCase.body)
			if response.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusNotImplemented, response.Body)
			}
			if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, "read-only") {
				t.Errorf("detail = %q, want it to name the writer this process holds none of", detail)
			}

			if served := get(t, server, testCase.served); served.Code == http.StatusNotImplemented {
				t.Errorf("%s answered %d, want the route this process holds a writer for to be served", testCase.served, served.Code)
			}
		})
	}
}

func TestProviderConfigCreateSummarisesWhyTheConfigIsUnready(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unready-hetzner"
	config := createProviderConfig(t, name)
	config.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "SecretUnresolved",
		Message:            `secret "horizon-hetzner" not found`,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: config.Generation,
	}}
	if err := testEnv.Client.Status().Update(t.Context(), config); err != nil {
		t.Fatalf("set the status of provider config %s: %v", name, err)
	}

	summary := summaryOf(t, newWritingServer(t), name)
	if reason := present(t, "reason", summary.Reason); reason != "SecretUnresolved" {
		t.Errorf("reason = %q, want %q", reason, "SecretUnresolved")
	}
	if message := present(t, "message", summary.Message); !strings.Contains(message, "horizon-hetzner") {
		t.Errorf("message = %q, want it to name the secret that could not be resolved", message)
	}
}

func referencedConfigFixture(name string) *v1alpha1.ProviderConfig {
	config := providerConfigFixture(name)
	config.Spec.Hetzner.NodeCredentialSecretRef = ptr(secretRef("horizon-hetzner-node", "ssh-key"))
	config.Spec.Hetzner.JoinTokenSecretRef = ptr(secretRef("horizon-join-token", "token"))
	config.Spec.Hetzner.SSHKeys = []string{"workstation"}
	config.Spec.Hetzner.Firewalls = []string{"burst"}
	return config
}

func createSecret(t *testing.T, name, key, value string) {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: metav1.NamespaceDefault},
		StringData: map[string]string{key: value},
	}
	if err := testEnv.Client.Create(t.Context(), secret); err != nil {
		t.Fatalf("create secret %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := testEnv.Client.Delete(context.Background(), secret); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete secret %s: %v", name, err)
		}
	})
}

func TestProviderConfigDetailRendersTheSpecAndItsConditions(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "detail-hetzner"
	config := createConfig(t, referencedConfigFixture(name))
	config.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "SecretUnresolved",
		Message:            `secret "horizon-join-token" not found`,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: config.Generation,
	}}
	if err := testEnv.Client.Status().Update(t.Context(), config); err != nil {
		t.Fatalf("set the status of provider config %s: %v", name, err)
	}

	response := get(t, newTestServer(t, testEnv.Client, AbsentCatalogue()), configEndpoint(name))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}

	detail := decodeBody[providerConfigDetailResponse](t, response)
	if detail.Summary.Name != name {
		t.Errorf("summary.name = %q, want %q", detail.Summary.Name, name)
	}
	if detail.Summary.Type != v1alpha1.ProviderTypeHetzner {
		t.Errorf("summary.type = %q, want %q", detail.Summary.Type, v1alpha1.ProviderTypeHetzner)
	}

	hetzner := present(t, "hetzner", detail.Hetzner)
	if hetzner.CredentialsSecretRef.Name != "horizon-hetzner" || hetzner.CredentialsSecretRef.Key != "token" {
		t.Errorf("credentialsSecretRef = %+v, want the name and key the spec carries", hetzner.CredentialsSecretRef)
	}
	if node := present(t, "nodeCredentialSecretRef", hetzner.NodeCredentialSecretRef); node.Key != "ssh-key" {
		t.Errorf("nodeCredentialSecretRef key = %q, want %q", node.Key, "ssh-key")
	}
	if image := present(t, "image", hetzner.Image); present(t, "image.name", image.Name) != "ubuntu-24.04" {
		t.Errorf("image = %+v, want it named ubuntu-24.04", image)
	}

	if detail.Watchdog.MaxLifetimeSeconds != seconds(8*time.Hour) {
		t.Errorf("maxLifetimeSeconds = %d, want %d", detail.Watchdog.MaxLifetimeSeconds, seconds(8*time.Hour))
	}

	ready := conditionEntryNamed(detail.Conditions, v1alpha1.ConditionReady)
	if ready == nil {
		t.Fatalf("the conditions omit %q", v1alpha1.ConditionReady)
	}
	if reason := present(t, "reason", ready.Reason); reason != "SecretUnresolved" {
		t.Errorf("reason = %q, want %q", reason, "SecretUnresolved")
	}
	if message := present(t, "message", ready.Message); !strings.Contains(message, "horizon-join-token") {
		t.Errorf("message = %q, want it to name the secret that could not be resolved", message)
	}
}

// the reference tells a missing secret apart from a misnamed one, and reading the secret itself would put a credential in a browser
func TestProviderConfigDetailNeverCarriesTheContentsOfAReferencedSecret(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "referenced-hetzner"
	const token = "a-token-no-response-may-repeat"
	createSecret(t, "horizon-hetzner", "token", token)
	createConfig(t, referencedConfigFixture(name))

	response := get(t, newTestServer(t, testEnv.Client, AbsentCatalogue()), configEndpoint(name))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	if body := response.Body.String(); strings.Contains(body, token) {
		t.Errorf("the detail carries the contents of the referenced secret: %s", body)
	}
}

func TestProviderConfigDetailQuotesEverySecretByNameAndKeyAlone(t *testing.T) {
	encoded, err := json.Marshal(newProviderConfigDetailResponse(referencedConfigFixture("quoted-hetzner"), time.Now()))
	if err != nil {
		t.Fatalf("marshal the detail: %v", err)
	}

	var decoded struct {
		Hetzner map[string]json.RawMessage `json:"hetzner"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode the detail: %v", err)
	}

	for _, field := range []string{
		"credentialsSecretRef",
		"nodeCredentialSecretRef",
		"joinTokenSecretRef",
		"cloudInitSecretRef",
	} {
		raw, carried := decoded.Hetzner[field]
		if !carried {
			t.Errorf("the detail omits %s", field)
			continue
		}
		var reference map[string]any
		if err := json.Unmarshal(raw, &reference); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
		if fields := slices.Sorted(maps.Keys(reference)); !slices.Equal(fields, []string{"key", "name"}) {
			t.Errorf("%s carries %v, want the name and the key alone", field, fields)
		}
	}
}

func TestProviderConfigDetailReportsAnUnreferencedOptionalSecret(t *testing.T) {
	config := providerConfigFixture("sparse-hetzner")

	detail := newProviderConfigDetailResponse(config, time.Now())

	hetzner := present(t, "hetzner", detail.Hetzner)
	if hetzner.NodeCredentialSecretRef != nil {
		t.Errorf("nodeCredentialSecretRef = %+v, want null", *hetzner.NodeCredentialSecretRef)
	}
	if hetzner.JoinTokenSecretRef != nil {
		t.Errorf("joinTokenSecretRef = %+v, want null", *hetzner.JoinTokenSecretRef)
	}
	if hetzner.SSHKeys == nil || len(hetzner.SSHKeys) != 0 {
		t.Errorf("sshKeys = %v, want an empty list rather than null", hetzner.SSHKeys)
	}
	if hetzner.Firewalls == nil || len(hetzner.Firewalls) != 0 {
		t.Errorf("firewalls = %v, want an empty list rather than null", hetzner.Firewalls)
	}
}

// a published catalogue runs to hundreds of entries, so the detail tallies it and the machines route lists it
func TestProviderConfigDetailTalliesThePublishedCatalogueByRegion(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	config := providerConfigFixture("stocked-hetzner")
	config.Status.InstanceTypes = []v1alpha1.InstanceType{
		publishedType("cx22", "nbg1"),
		publishedType("cx32", "nbg1"),
		publishedType("cx22", "fsn1"),
	}
	config.Status.CatalogueRefreshedAt = &metav1.Time{Time: now.Add(-time.Minute)}

	catalogue := newProviderConfigDetailResponse(config, now).Catalogue

	if catalogue.Types != 3 {
		t.Errorf("types = %d, want 3", catalogue.Types)
	}
	want := []catalogueRegion{{Region: "fsn1", Types: 1}, {Region: "nbg1", Types: 2}}
	if !slices.Equal(catalogue.Regions, want) {
		t.Errorf("regions = %+v, want %+v in name order", catalogue.Regions, want)
	}
	if refreshed := present(t, "refreshedAt", catalogue.RefreshedAt); refreshed != rfc3339(now.Add(-time.Minute)) {
		t.Errorf("refreshedAt = %q, want %q", refreshed, rfc3339(now.Add(-time.Minute)))
	}
}

func TestProviderConfigDetailReportsAnUnpublishedCatalogue(t *testing.T) {
	catalogue := newProviderConfigDetailResponse(providerConfigFixture("fresh-hetzner"), time.Now()).Catalogue

	if catalogue.Types != 0 {
		t.Errorf("types = %d, want none", catalogue.Types)
	}
	if catalogue.Regions == nil || len(catalogue.Regions) != 0 {
		t.Errorf("regions = %v, want an empty list rather than null", catalogue.Regions)
	}
	if catalogue.RefreshedAt != nil {
		t.Errorf("refreshedAt = %q, want null", *catalogue.RefreshedAt)
	}
}

func TestProviderConfigDetailReportsAMissingConfig(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	response := get(t, newTestServer(t, testEnv.Client, AbsentCatalogue()), configEndpoint("absent-hetzner"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusNotFound, response.Body)
	}
	if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, "absent-hetzner") {
		t.Errorf("detail = %q, want the missing config named", detail)
	}
}

func TestProviderConfigDetailReportsAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	response := get(t, server, configEndpoint("unreadable-hetzner"))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusBadGateway, response.Body)
	}
}

func bindLeaseTo(t *testing.T, config string) string {
	t.Helper()
	name := config + "-holder"
	createLease(t, &v1alpha1.CapacityLease{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CapacityLeaseSpec{
			ProviderRef: config,
			Region:      "nbg1",
			Size:        "cx22",
			Replicas:    1,
			Duration:    metav1.Duration{Duration: time.Hour},
		},
	}, v1alpha1.CapacityLeaseStatus{})
	return name
}

func summaryOf(t *testing.T, server *Server, name string) providerConfigSummary {
	t.Helper()
	response := get(t, server, machinesEndpoint)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}

	summaries := decodeBody[machineCatalogueResponse](t, response).Configs
	for i := range summaries {
		if summaries[i].Name == name {
			return summaries[i]
		}
	}
	t.Fatalf("the machine catalogue lists %v, want it to carry %s", summaries, name)
	return providerConfigSummary{}
}

func TestProviderConfigReplaceStoresTheWholeSubmittedSpec(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "replaced-hetzner"
	createProviderConfig(t, name)

	replacement := configSpecFixture()
	replacement.Hetzner.Image = "debian-13"
	replacement.Hetzner.SSHKeys = nil
	replacement.Hetzner.JoinTokenSecretRef = nil

	response := mutate(t, newWritingServer(t), http.MethodPut, configEndpoint(name), replacement)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	if named := decodeBody[providerConfigSummary](t, response).Name; named != name {
		t.Errorf("the response names %q, want %q", named, name)
	}

	hetzner := present(t, "hetzner", readConfig(t, name).Spec.Hetzner)
	if image := present(t, "image", hetzner.Image); image.Name != "debian-13" {
		t.Errorf("image names %q, want %q", image.Name, "debian-13")
	}
	if node := present(t, "nodeCredentialSecretRef", hetzner.NodeCredentialSecretRef); node.Name != "horizon-hetzner-node" {
		t.Errorf("nodeCredentialSecretRef names %q, want the submitted one", node.Name)
	}
	if hetzner.JoinTokenSecretRef != nil {
		t.Errorf("joinTokenSecretRef = %+v, want a replacement that omits it to clear it", hetzner.JoinTokenSecretRef)
	}
	if len(hetzner.SSHKeys) != 0 {
		t.Errorf("sshKeys = %v, want a replacement that omits them to clear them", hetzner.SSHKeys)
	}
}

func TestProviderConfigReplaceRefusesAChangeMeasuredAgainstAStaleRead(t *testing.T) {
	// a compare and swap belongs to the verb, so two edits measured against the same read cannot silently net out
	testEnv.SkipUnlessRunning(t)

	const name = "raced-hetzner"
	config := createProviderConfig(t, name)
	stale := config.ResourceVersion

	config.Spec.Watchdog.Slack = metav1.Duration{Duration: 3 * time.Minute}
	if err := testEnv.Client.Update(t.Context(), config); err != nil {
		t.Fatalf("move provider config %s on: %v", name, err)
	}

	options := writingOptions()
	options.ConfigWriter = ProviderConfigWriterFor(staleReadCluster{Client: testEnv.Client, revision: stale})
	server, err := New(options)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)

	response := mutate(t, server, http.MethodPut, configEndpoint(name), configSpecFixture())
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusConflict, response.Body)
	}
	if slack := readConfig(t, name).Spec.Watchdog.Slack.Duration; slack != 3*time.Minute {
		t.Errorf("slack = %s, want the 3m the competing change wrote to stand", slack)
	}
}

func TestProviderConfigReplaceRefusesAnAbsentConfig(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	response := mutate(t, newWritingServer(t), http.MethodPut, configEndpoint("never-created"), configSpecFixture())
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusNotFound, response.Body)
	}
}

func TestProviderConfigReplaceSurfacesTheWatchdogRefusalVerbatim(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unwatchable-hetzner"
	createProviderConfig(t, name)

	replacement := configSpecFixture()
	replacement.Watchdog.SlackSeconds = seconds(30 * time.Second)

	response := mutate(t, newWritingServer(t), http.MethodPut, configEndpoint(name), replacement)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}
	if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, "slack must be greater than renewInterval") {
		t.Errorf("detail = %q, want the message the apiserver rejected it with", detail)
	}
}

func TestProviderConfigReplaceRefusesABodyCarryingAnythingElse(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unknown-field-replacement"
	createProviderConfig(t, name)

	body := map[string]any{"type": v1alpha1.ProviderTypeHetzner, "credentials": "a token"}
	response := mutate(t, newWritingServer(t), http.MethodPut, configEndpoint(name), body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusBadRequest, response.Body)
	}
}

func TestProviderConfigDeleteIsRefusedWhileALeaseNamesIt(t *testing.T) {
	// deleting the credentials horizon tears a lease down with is the worst thing this interface could do, so the bound leases are named rather than a teardown promised
	testEnv.SkipUnlessRunning(t)

	const name = "bound-hetzner"
	createProviderConfig(t, name)
	lease := bindLeaseTo(t, name)

	response := mutate(t, newWritingServer(t), http.MethodDelete, configEndpoint(name), nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusConflict, response.Body)
	}
	if detail := decodeBody[apiError](t, response).Detail; !strings.Contains(detail, lease) {
		t.Errorf("detail = %q, want it to name the lease holding the config", detail)
	}
	if readConfig(t, name).DeletionTimestamp != nil {
		t.Errorf("provider config %s is being deleted, want the refusal to have written nothing", name)
	}
}

func TestProviderConfigDeleteAsksTheClusterOnceNoLeaseNamesIt(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "unbound-hetzner"
	createProviderConfig(t, name)
	bindLeaseTo(t, "another-hetzner")

	response := mutate(t, newWritingServer(t), http.MethodDelete, configEndpoint(name), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusAccepted, response.Body)
	}
	if named := decodeBody[providerConfigDeleteResponse](t, response).Name; named != name {
		t.Errorf("the response names %q, want %q", named, name)
	}
	err := testEnv.Client.Get(t.Context(), client.ObjectKey{Name: name}, &v1alpha1.ProviderConfig{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("reading the deleted config answered %v, want a not-found", err)
	}
}

func TestProviderConfigDeleteRefusesAnAbsentConfig(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	response := mutate(t, newWritingServer(t), http.MethodDelete, configEndpoint("never-created"), nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusNotFound, response.Body)
	}
}

func TestProviderConfigWritesAreBehindTheCrossOriginGuard(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "guarded-hetzner"
	createProviderConfig(t, name)
	server := newWritingServer(t)

	for label, request := range map[string]*http.Request{
		"replace": newMutation(t, http.MethodPut, configEndpoint(name), configSpecFixture()),
		"delete":  newMutation(t, http.MethodDelete, configEndpoint(name), nil),
	} {
		t.Run(label, func(t *testing.T) {
			request.Header.Del(interfaceHeader)

			if response := send(server, request); response.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d, body %s", response.Code, http.StatusForbidden, response.Body)
			}
			live := readConfig(t, name)
			if live.DeletionTimestamp != nil || present(t, "image", live.Spec.Hetzner.Image).Name != "ubuntu-24.04" {
				t.Errorf("provider config %s moved, want a refused request to have written nothing", name)
			}
		})
	}
}

func TestProviderConfigSummaryCarriesTheSpecInTheShapeAWriteAccepts(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	const name = "summarised-hetzner"
	createProviderConfig(t, name)

	spec := present(t, "spec", summaryOf(t, newWritingServer(t), name).Spec)
	if spec.Type != v1alpha1.ProviderTypeHetzner {
		t.Errorf("type = %q, want %q", spec.Type, v1alpha1.ProviderTypeHetzner)
	}
	hetzner := present(t, "hetzner", spec.Hetzner)
	if hetzner.Image != "ubuntu-24.04" {
		t.Errorf("image = %q, want %q", hetzner.Image, "ubuntu-24.04")
	}
	if hetzner.CredentialsSecretRef.Name != "horizon-hetzner" {
		t.Errorf("credentialsSecretRef names %q, want %q", hetzner.CredentialsSecretRef.Name, "horizon-hetzner")
	}
	if spec.Watchdog.MaxLifetimeSeconds != seconds(8*time.Hour) {
		t.Errorf("maxLifetimeSeconds = %d, want %d", spec.Watchdog.MaxLifetimeSeconds, seconds(8*time.Hour))
	}
}

func TestProviderConfigSummaryReportsNoSpecForAConfigThisShapeCannotCarry(t *testing.T) {
	// a configuration written with anything this shape cannot express is reported as unrepresentable rather than reshaped into one an edit would silently rewrite
	testEnv.SkipUnlessRunning(t)

	const name = "selected-image-hetzner"
	config := createProviderConfig(t, name)
	config.Spec.Hetzner.Image = nil
	config.Spec.Hetzner.ImageSelector = map[string]string{"name": "bedrock-node"}
	if err := testEnv.Client.Update(t.Context(), config); err != nil {
		t.Fatalf("point provider config %s at an image selector: %v", name, err)
	}

	if spec := summaryOf(t, newWritingServer(t), name).Spec; spec != nil {
		t.Errorf("spec = %+v, want null for a config this shape cannot carry", spec)
	}
}
