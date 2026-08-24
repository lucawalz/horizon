package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/catalogue"
	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/testenv"
)

// the guard is anchored to the address a listener bound, so a handler-level test states that address rather than deriving it from the request it is about to send
const (
	servedTestHost   = "127.0.0.1:8973"
	servedTestOrigin = "http://127.0.0.1:8973"
)

var testEnv *testenv.Environment

func TestMain(m *testing.M) {
	env, err := testenv.Start(clusterScheme())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testEnv = env

	code := m.Run()

	if err := env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
	}
	os.Exit(code)
}

func clusterScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

type stubCatalogue struct {
	types  []provider.InstanceType
	err    error
	age    time.Duration
	filled bool
}

func (s stubCatalogue) List(string, string) ([]provider.InstanceType, error) {
	return s.types, s.err
}

func (s stubCatalogue) Age(string) (time.Duration, bool) { return s.age, s.filled }

type failingReader struct{ err error }

func (f failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return f.err
}

func (f failingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return f.err
}

func newTestServer(t *testing.T, reader client.Reader, types catalogue.Reader) *Server {
	t.Helper()
	server, err := New(Options{Client: reader, Catalogue: types})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	anchor(t, server)
	return server
}

func anchor(t *testing.T, server *Server) {
	t.Helper()
	if err := server.anchorTo(servedTestHost); err != nil {
		t.Fatalf("anchor the server to %s: %v", servedTestHost, err)
	}
}

func get(t *testing.T, server *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

// the recorder keeps mutating its live header map after WriteHeader, so only the snapshot shows what reached the client
func sentHeaders(response *httptest.ResponseRecorder) http.Header {
	return response.Result().Header
}

func decodeBody[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return body
}

func present[T any](t *testing.T, field string, held *T) T {
	t.Helper()
	if held == nil {
		var missing T
		t.Fatalf("%s is null, want a value", field)
		return missing
	}
	return *held
}

func parseInstant(t *testing.T, field, encoded string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, encoded)
	if err != nil {
		t.Fatalf("parse %s %q: %v", field, encoded, err)
	}
	return at
}

// a test may release the lease it created, so the cleanup treats an absent one as already gone
func removeAfterTest(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		lease := &v1alpha1.CapacityLease{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if err := testEnv.Client.Delete(context.Background(), lease); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete lease %s: %v", name, err)
		}
	})
}

func createLease(t *testing.T, lease *v1alpha1.CapacityLease, status v1alpha1.CapacityLeaseStatus) {
	t.Helper()
	ctx := t.Context()
	if err := testEnv.Client.Create(ctx, lease); err != nil {
		t.Fatalf("create lease %s: %v", lease.Name, err)
	}
	removeAfterTest(t, lease.Name)

	lease.Status = status
	if err := testEnv.Client.Status().Update(ctx, lease); err != nil {
		t.Fatalf("set the status of lease %s: %v", lease.Name, err)
	}
}

func secretRef(name, key string) corev1.SecretKeySelector {
	return corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name},
		Key:                  key,
	}
}

func createProviderConfig(t *testing.T, name string) *v1alpha1.ProviderConfig {
	t.Helper()
	config := &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ProviderConfigSpec{
			Type: v1alpha1.ProviderTypeHetzner,
			Hetzner: &v1alpha1.HetznerProviderSpec{
				CredentialsSecretRef: secretRef("horizon-hetzner", "token"),
				CloudInitSecretRef:   secretRef("horizon-cloud-init", "user-data"),
				Image:                &v1alpha1.ImageSpec{Name: "ubuntu-24.04"},
			},
			Watchdog: v1alpha1.WatchdogPolicy{
				RenewInterval: metav1.Duration{Duration: time.Minute},
				Slack:         metav1.Duration{Duration: 2 * time.Minute},
				MaxLifetime:   metav1.Duration{Duration: 8 * time.Hour},
			},
		},
	}
	if err := testEnv.Client.Create(t.Context(), config); err != nil {
		t.Fatalf("create provider config %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := testEnv.Client.Delete(context.Background(), config); err != nil {
			t.Errorf("delete provider config %s: %v", name, err)
		}
	})
	return config
}
