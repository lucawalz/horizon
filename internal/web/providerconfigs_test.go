package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const providerConfigsEndpoint = "/api/providerconfigs"

func configRequestFixture(name string) providerConfigCreateRequest {
	return providerConfigCreateRequest{
		Name: name,
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

func removeConfigAfterTest(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		config := &v1alpha1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if err := testEnv.Client.Delete(context.Background(), config); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete provider config %s: %v", name, err)
		}
	})
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

	response := get(t, newWritingServer(t), machinesEndpoint)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}

	summaries := decodeBody[machineCatalogueResponse](t, response).Configs
	var summary *providerConfigSummary
	for i := range summaries {
		if summaries[i].Name == name {
			summary = &summaries[i]
		}
	}
	if summary == nil {
		t.Fatalf("the machine catalogue lists %v, want it to carry %s", summaries, name)
	}
	if reason := present(t, "reason", summary.Reason); reason != "SecretUnresolved" {
		t.Errorf("reason = %q, want %q", reason, "SecretUnresolved")
	}
	if message := present(t, "message", summary.Message); !strings.Contains(message, "horizon-hetzner") {
		t.Errorf("message = %q, want it to name the secret that could not be resolved", message)
	}
}
