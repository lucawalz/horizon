// Package agent runs the node-side dead man's switch that destroys a leased server.
package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lucawalz/horizon/internal/provider"
	"github.com/lucawalz/horizon/internal/provider/hetzner"
)

const (
	minMaxLifetime      = 5 * time.Minute
	maxMaxLifetime      = 24 * time.Hour
	metadataHTTPTimeout = 10 * time.Second

	DefaultPollInterval = 15 * time.Second
)

type Options struct {
	MaxLifetime     time.Duration
	PollInterval    time.Duration
	TokenPath       string
	KubeconfigPath  string
	NodeName        string
	MetadataBaseURL string
	StateDir        string
}

func (o Options) Validate() error {
	if o.MaxLifetime < minMaxLifetime || o.MaxLifetime > maxMaxLifetime {
		return fmt.Errorf("agent: max lifetime %s is outside the supported range %s to %s",
			o.MaxLifetime, minMaxLifetime, maxMaxLifetime)
	}
	if o.PollInterval <= 0 {
		return fmt.Errorf("agent: poll interval %s must be greater than zero", o.PollInterval)
	}
	if o.TokenPath == "" {
		return fmt.Errorf("agent: token path must not be empty")
	}
	if o.KubeconfigPath == "" {
		return fmt.Errorf("agent: kubeconfig path must not be empty")
	}
	if o.MetadataBaseURL == "" {
		return fmt.Errorf("agent: metadata url must not be empty")
	}
	if o.StateDir == "" {
		return fmt.Errorf("agent: state directory must not be empty")
	}
	return nil
}

func Run(ctx context.Context, opts Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	ctrl.SetLogger(klog.Background())
	log := ctrl.LoggerFrom(ctx)

	token, err := readToken(opts.TokenPath)
	if err != nil {
		return err
	}

	identity, err := resolveIdentity(ctx, opts.MetadataBaseURL, &http.Client{Timeout: metadataHTTPTimeout})
	if err != nil {
		return err
	}
	if opts.NodeName != "" {
		identity.Name = opts.NodeName
	}

	prov, err := hetzner.NewNodeClient(token)
	if err != nil {
		return err
	}
	inst, err := proveIdentity(ctx, prov, identity)
	if err != nil {
		return err
	}

	if terminationRecorded(opts.StateDir) {
		log.Info("resuming a teardown recorded by an earlier run", "instance", identity.Name)
		return destroy(ctx, prov, identity.Name, opts.StateDir, destroyBackoff())
	}

	log.Info("armed", "instance", identity.Name, "maxLifetime", opts.MaxLifetime)
	startedAt := time.Now()
	wall := newNodeDeadline(opts.KubeconfigPath, identity.Name, seedWallDeadline(ctx, inst))
	wall.markArmed(ctx, startedAt)
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			reason, due := fired(startedAt, opts.MaxLifetime, wall.read(ctx), now)
			if due {
				log.Info("destroying this server", "instance", identity.Name, "reason", reason)
				return destroy(ctx, prov, identity.Name, opts.StateDir, destroyBackoff())
			}
			wall.markArmed(ctx, now)
		}
	}
}

func proveIdentity(ctx context.Context, prov provider.Provider, identity Identity) (provider.Instance, error) {
	inst, err := prov.Get(ctx, identity.Name)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("agent: read instance %q back from the provider: %w", identity.Name, err)
	}
	want := hetzner.ProviderIDPrefix + identity.InstanceID
	if inst.ProviderID != want {
		return provider.Instance{}, fmt.Errorf("agent: instance %q reports provider id %q but the metadata service reports %q",
			identity.Name, inst.ProviderID, want)
	}
	return inst, nil
}

func seedWallDeadline(ctx context.Context, inst provider.Instance) *time.Time {
	log := ctrl.LoggerFrom(ctx)
	deadline, readable := provider.ParseExpiry(inst.Labels)
	if !readable {
		log.Info("no readable expiry label on this instance, leaving the backstop as the only clock",
			"instance", inst.Name, "label", provider.ExpiresAtLabelKey)
		return nil
	}
	log.Info("seeded the wall clock deadline from the instance label",
		"instance", inst.Name, "deadline", deadline)
	return &deadline
}

func readToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agent: read token %q: %w", path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("agent: token file %q is empty", path)
	}
	return token, nil
}

func waitBeforeRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
