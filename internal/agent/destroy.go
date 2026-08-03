package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/provider"
)

const (
	terminatingSentinel = "terminating"
	stateDirMode        = 0o700
	sentinelMode        = 0o600

	destroyRetryInitial = 5 * time.Second
	destroyRetryFactor  = 2
	destroyRetryCap     = time.Minute
	destroyRetrySteps   = math.MaxInt32
)

func destroyBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: destroyRetryInitial,
		Factor:   destroyRetryFactor,
		Cap:      destroyRetryCap,
		Steps:    destroyRetrySteps,
	}
}

func destroy(ctx context.Context, prov provider.Provider, name string, stateDir string, backoff wait.Backoff) error {
	if err := markTerminating(stateDir); err != nil {
		return err
	}

	log := ctrl.LoggerFrom(ctx)
	var lastErr error
	for attempt := range backoff.Steps {
		if attempt > 0 {
			if err := waitBeforeRetry(ctx, backoff.Step()); err != nil {
				return fmt.Errorf("agent: destroy instance %q: %w", name, err)
			}
		}
		absent, err := deleteAndConfirm(ctx, prov, name)
		if absent {
			return nil
		}
		lastErr = err
		if err != nil {
			log.Error(err, "self destruct attempt failed", "instance", name)
			continue
		}
		log.Info("instance still present after delete", "instance", name)
	}

	if lastErr != nil {
		return fmt.Errorf("agent: destroy instance %q: %w", name, lastErr)
	}
	return fmt.Errorf("agent: instance %q is still present after %d delete attempts", name, backoff.Steps)
}

func deleteAndConfirm(ctx context.Context, prov provider.Provider, name string) (bool, error) {
	if err := prov.Delete(ctx, name); err != nil {
		return false, fmt.Errorf("agent: delete instance %q: %w", name, err)
	}
	return provider.ConfirmAbsent(ctx, prov, name)
}

func markTerminating(stateDir string) error {
	if err := os.MkdirAll(stateDir, stateDirMode); err != nil {
		return fmt.Errorf("agent: create state directory %q: %w", stateDir, err)
	}
	path := sentinelPath(stateDir)
	if err := os.WriteFile(path, nil, sentinelMode); err != nil {
		return fmt.Errorf("agent: record teardown in %q: %w", path, err)
	}
	return nil
}

func terminationRecorded(stateDir string) bool {
	_, err := os.Stat(sentinelPath(stateDir))
	return err == nil
}

func sentinelPath(stateDir string) string {
	return filepath.Join(stateDir, terminatingSentinel)
}
