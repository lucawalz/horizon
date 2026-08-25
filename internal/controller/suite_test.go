package controller

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/testenv"
)

var testEnv *testenv.Environment

func TestMain(m *testing.M) {
	env, err := testenv.Start(testScheme())
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

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func apiServerClient(t *testing.T) client.Client {
	t.Helper()
	testEnv.SkipUnlessRunning(t)
	return testEnv.Client
}

// the CRD bounds a lease name at the label value limit, and a test name is longer than that often enough to matter
const maxLeaseNameLength = 63

var nonNameCharacters = regexp.MustCompile(`[^a-z0-9]+`)

func objectName(t *testing.T) string {
	t.Helper()
	return boundedName(nonNameCharacters.ReplaceAllString(strings.ToLower(t.Name()), "-"))
}

func suffixedObjectName(t *testing.T, suffix string) string {
	t.Helper()
	// the suffix is what tells two objects of one test apart, so the room for it comes out of the name rather than out of the suffix
	base := objectName(t)
	if room := maxLeaseNameLength - len(suffix) - 1; len(base) > room {
		base = base[:room]
	}
	return boundedName(base + "-" + suffix)
}

func boundedName(name string) string {
	trimmed := strings.Trim(name, "-")
	if len(trimmed) <= maxLeaseNameLength {
		return trimmed
	}
	return strings.Trim(trimmed[:maxLeaseNameLength], "-")
}

func assertUpdate(t *testing.T, c client.Client, obj client.Object, wantRejected bool) {
	t.Helper()
	err := c.Update(t.Context(), obj)
	switch {
	case wantRejected && err == nil:
		t.Fatalf("apiserver accepted an update to %T, want rejection", obj)
	case !wantRejected && err != nil:
		t.Fatalf("apiserver rejected an update to %T: %v", obj, err)
	}
}

func assertCreate(t *testing.T, c client.Client, obj client.Object, wantRejected bool) {
	t.Helper()
	err := c.Create(t.Context(), obj)
	if err == nil {
		t.Cleanup(func() { _ = c.Delete(context.Background(), obj) })
	}
	switch {
	case wantRejected && err == nil:
		t.Fatalf("apiserver accepted %T, want rejection", obj)
	case !wantRejected && err != nil:
		t.Fatalf("apiserver rejected %T: %v", obj, err)
	}
}
