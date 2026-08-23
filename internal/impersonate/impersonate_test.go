package impersonate

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/testenv"
	"github.com/lucawalz/horizon/internal/web"
)

const (
	testHost      = "https://127.0.0.1:6443"
	testUser      = "ada"
	testGroup     = "platform"
	concurrentRun = 64
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

func testIdentity() web.Identity {
	return web.Identity{Username: testUser, Groups: []string{testGroup}}
}

func TestTheDerivedConfigCarriesTheIdentityAsImpersonation(t *testing.T) {
	groups := []string{testGroup, "oncall"}

	config := as(&rest.Config{Host: testHost}, web.Identity{Username: testUser, Groups: groups})

	if config.Impersonate.UserName != testUser {
		t.Errorf("impersonated user = %q, want %q", config.Impersonate.UserName, testUser)
	}
	if !slices.Equal(config.Impersonate.Groups, groups) {
		t.Errorf("impersonated groups = %v, want %v", config.Impersonate.Groups, groups)
	}
}

// every request derives from one shared config, so a config mutated in place would carry one caller's identity into another's request
func TestTheBaseConfigIsNeverImpersonated(t *testing.T) {
	base := &rest.Config{Host: testHost}

	as(base, testIdentity())

	if base.Impersonate.UserName != "" || len(base.Impersonate.Groups) != 0 {
		t.Errorf("the base config carries %+v, want it untouched", base.Impersonate)
	}
}

func TestConcurrentIdentitiesNeverCrossOver(t *testing.T) {
	base := &rest.Config{Host: testHost}
	crossings := make(chan string, 2*concurrentRun)
	start := make(chan struct{})

	var inFlight sync.WaitGroup
	for i := range concurrentRun {
		identity := web.Identity{
			Username: fmt.Sprintf("user-%d", i),
			Groups:   []string{fmt.Sprintf("group-%d", i)},
		}
		inFlight.Add(1)
		go func() {
			defer inFlight.Done()
			<-start
			config := as(base, identity)
			if config.Impersonate.UserName != identity.Username {
				crossings <- fmt.Sprintf("%s was built as %q", identity.Username, config.Impersonate.UserName)
			}
			if !slices.Equal(config.Impersonate.Groups, identity.Groups) {
				crossings <- fmt.Sprintf("%s was built with groups %v", identity.Username, config.Impersonate.Groups)
			}
		}()
	}
	close(start)
	inFlight.Wait()
	close(crossings)

	for crossing := range crossings {
		t.Error(crossing)
	}
	if base.Impersonate.UserName != "" {
		t.Errorf("the base config carries %+v, want it untouched", base.Impersonate)
	}
}

// an empty username sends no impersonation header at all, so the request would reach the cluster as this process
func TestAnIdentityWithNoNameIsRefused(t *testing.T) {
	clients, err := New(&rest.Config{Host: testHost}, clusterScheme())
	if err != nil {
		t.Fatalf("new clients: %v", err)
	}

	if _, err := clients.ReaderFor(web.Identity{Groups: []string{testGroup}}); err == nil {
		t.Error("a reader was built for an unnamed identity, want a refusal")
	}
	if _, err := clients.WriterFor(web.Identity{Groups: []string{testGroup}}); err == nil {
		t.Error("a writer was built for an unnamed identity, want a refusal")
	}
}

func TestNewRefusesAnAbsentConfigOrScheme(t *testing.T) {
	if _, err := New(nil, clusterScheme()); err == nil {
		t.Error("clients were built without a config, want a refusal")
	}
	if _, err := New(&rest.Config{Host: testHost}, nil); err == nil {
		t.Error("clients were built without a scheme, want a refusal")
	}
}

func TestTheImpersonatedClientReachesTheClusterAsTheIdentity(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	clients, err := New(testEnv.Config, clusterScheme())
	if err != nil {
		t.Fatalf("new clients: %v", err)
	}
	writer, err := clients.WriterFor(testIdentity())
	if err != nil {
		t.Fatalf("writer for %s: %v", testUser, err)
	}

	review := &authenticationv1.SelfSubjectReview{}
	if err := writer.Create(t.Context(), review); err != nil {
		t.Fatalf("review the impersonated subject: %v", err)
	}

	if review.Status.UserInfo.Username != testUser {
		t.Errorf("the cluster saw %q, want %q", review.Status.UserInfo.Username, testUser)
	}
	if !slices.Contains(review.Status.UserInfo.Groups, testGroup) {
		t.Errorf("the cluster saw groups %v, want them to include %q", review.Status.UserInfo.Groups, testGroup)
	}
}

// the adopter's RBAC decides what an impersonated caller may read, so a user with no binding is refused by the cluster itself
func TestTheClusterRefusesAnImpersonatedReaderWithoutRBAC(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	clients, err := New(testEnv.Config, clusterScheme())
	if err != nil {
		t.Fatalf("new clients: %v", err)
	}
	reader, err := clients.ReaderFor(testIdentity())
	if err != nil {
		t.Fatalf("reader for %s: %v", testUser, err)
	}

	var leases v1alpha1.CapacityLeaseList
	err = reader.List(t.Context(), &leases)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("listing the leases as %s returned %v, want a forbidden refusal", testUser, err)
	}
	if !strings.Contains(err.Error(), testUser) {
		t.Errorf("the refusal reads %q, want it to name %s", err, testUser)
	}
}
