package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const seededNamespacePrefix = "horizon-namespace-list-"

type namespaceReader struct{ names []string }

func (namespaceReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("web: this reader answers only a namespace list")
}

func (n namespaceReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	namespaces, listed := list.(*corev1.NamespaceList)
	if !listed {
		return fmt.Errorf("web: this reader answers only a namespace list, not %T", list)
	}
	for _, name := range n.names {
		namespaces.Items = append(namespaces.Items, corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	return nil
}

func seedNamespace(t *testing.T) string {
	t.Helper()
	// envtest runs no namespace controller, so a deleted namespace never leaves Terminating and a fresh name is generated per run instead
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: seededNamespacePrefix}}
	if err := testEnv.Client.Create(t.Context(), namespace); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return namespace.Name
}

func TestNamespacesListsTheNamespacesOfTheCluster(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	seeded := seedNamespace(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	response := get(t, server, namespacesEndpoint)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	listed := decodeBody[namespaceListResponse](t, response).Namespaces
	for _, want := range []string{"default", "kube-system", seeded} {
		if !slices.Contains(listed, want) {
			t.Errorf("namespaces = %v, want it to carry %q", listed, want)
		}
	}
}

func TestNamespacesAreSortedSoTheSuggestionsHoldStill(t *testing.T) {
	server := newTestServer(t, namespaceReader{names: []string{"staging", "batch", "default"}}, AbsentCatalogue())

	response := get(t, server, namespacesEndpoint)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	listed := decodeBody[namespaceListResponse](t, response).Namespaces
	if want := []string{"batch", "default", "staging"}; !slices.Equal(listed, want) {
		t.Errorf("namespaces = %v, want %v", listed, want)
	}
}

func TestNamespacesAnswersAnEmptyClusterWithAnEmptyList(t *testing.T) {
	server := newTestServer(t, namespaceReader{}, AbsentCatalogue())

	response := get(t, server, namespacesEndpoint)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body %s", response.Code, http.StatusOK, response.Body)
	}
	if body := response.Body.String(); !strings.Contains(body, `"namespaces":[]`) {
		t.Errorf("body = %s, want an empty array rather than null", body)
	}
}

func TestNamespacesReportsAClusterFailure(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("the api server is unreachable")}, AbsentCatalogue())

	response := get(t, server, namespacesEndpoint)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if failure := decodeBody[apiError](t, response); failure.Status != http.StatusBadGateway {
		t.Errorf("body status = %d, want %d", failure.Status, http.StatusBadGateway)
	}
}

func TestNamespacesRefusesAnyVerbThatCouldMutate(t *testing.T) {
	server := newTestServer(t, namespaceReader{names: []string{"batch"}}, AbsentCatalogue())

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			response := send(server, httptest.NewRequest(method, namespacesEndpoint, nil))

			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d so the route answers for itself rather than reaching the mutating path",
					response.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}
