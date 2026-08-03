package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/fake"
)

const testDestroySteps = 5

func instantBackoff() wait.Backoff {
	return wait.Backoff{Steps: testDestroySteps}
}

func TestDestroyDeletesOnTheFirstAttempt(t *testing.T) {
	prov := fake.New()
	prov.Seed(provider.Instance{
		Name:   "burst-0",
		Labels: map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue},
	})

	if err := destroy(context.Background(), prov, "burst-0", t.TempDir(), instantBackoff()); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if got := len(prov.DeleteCalls()); got != 1 {
		t.Errorf("delete calls = %d, want 1", got)
	}
}

func TestDestroyRetriesUntilTheProviderAcceptsTheDelete(t *testing.T) {
	const failures = 3
	prov := fake.New()
	prov.Seed(provider.Instance{
		Name:   "burst-0",
		Labels: map[string]string{provider.ManagedByLabelKey: provider.ManagedByValue},
	})

	var attempts atomic.Int32
	prov.FailDelete = func(string) error {
		if attempts.Add(1) <= failures {
			return errors.New("provider is unreachable")
		}
		return nil
	}

	if err := destroy(context.Background(), prov, "burst-0", t.TempDir(), instantBackoff()); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if got := attempts.Load(); got != failures+1 {
		t.Errorf("delete attempts = %d, want %d", got, failures+1)
	}
}

type stubbornProvider struct {
	provider.Provider
	deletes atomic.Int32
	gets    atomic.Int32
}

func (p *stubbornProvider) Delete(context.Context, string) error {
	p.deletes.Add(1)
	return nil
}

func (p *stubbornProvider) Get(_ context.Context, name string) (provider.Instance, error) {
	p.gets.Add(1)
	return provider.Instance{Name: name}, nil
}

func TestDestroyReportsFailureWhileTheInstanceStaysVisible(t *testing.T) {
	prov := &stubbornProvider{}

	if err := destroy(context.Background(), prov, "burst-0", t.TempDir(), instantBackoff()); err == nil {
		t.Fatal("destroy must not report success while the provider still returns the instance")
	}
	if got := prov.deletes.Load(); got != testDestroySteps {
		t.Errorf("delete calls = %d, want %d", got, testDestroySteps)
	}
	if got := prov.gets.Load(); got != testDestroySteps {
		t.Errorf("absence checks = %d, want %d", got, testDestroySteps)
	}
}

func TestDestroyRecordsTheTeardownBeforeTheFirstDelete(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	prov := fake.New()
	prov.FailDelete = func(string) error { return errors.New("provider is unreachable") }

	if err := destroy(context.Background(), prov, "burst-0", stateDir, wait.Backoff{Steps: 1}); err == nil {
		t.Fatal("destroy must report the failing provider")
	}
	if _, err := os.Stat(filepath.Join(stateDir, terminatingSentinel)); err != nil {
		t.Fatalf("sentinel is missing after the first delete attempt: %v", err)
	}
	if !terminationRecorded(stateDir) {
		t.Error("terminationRecorded must see the sentinel destroy wrote")
	}
}
