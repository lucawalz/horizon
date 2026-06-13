package hetzner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/wireguard"
)

const sharedInfraTimeout = 10 * time.Minute

type Provider struct {
	cfg          *config.Config
	workDir      string
	burstID      string
	wgKeypair    wireguard.Keypair
	wgGenerated  bool
	wgIP         string
	hubPublicKey string
	sshPublicKey string
	k3sURL       string
	k3sToken     string
	serverID     string
	serverIP     string
}

func New(cfg *config.Config, workDir string) *Provider {
	return &Provider{cfg: cfg, workDir: workDir, burstID: newBurstID()}
}

func newBurstID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

var internalBurstIDPattern = regexp.MustCompile(`^[a-f0-9]{4,16}$`)

func NewWithBurstID(cfg *config.Config, workDir, burstID string) (*Provider, error) {
	if !internalBurstIDPattern.MatchString(burstID) {
		return nil, fmt.Errorf("hetzner: NewWithBurstID: invalid burst_id %q", burstID)
	}
	return &Provider{cfg: cfg, workDir: workDir, burstID: burstID}, nil
}

func (p *Provider) SetRuntimeSecrets(hubPublicKey, wgIP, sshPublicKey, k3sURL, k3sToken string) {
	p.hubPublicKey = hubPublicKey
	p.wgIP = wgIP
	p.sshPublicKey = sshPublicKey
	p.k3sURL = k3sURL
	p.k3sToken = k3sToken
}

func (p *Provider) GenerateTFVars() (map[string]string, error) {
	if p.hubPublicKey == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing wireguard hub public key (call SetRuntimeSecrets first)")
	}
	if p.wgIP == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing wireguard ip")
	}
	if p.sshPublicKey == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing ssh public key")
	}
	if p.k3sURL == "" || p.k3sToken == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing k3s url or token")
	}
	return map[string]string{
		"burst_id":       p.burstID,
		"server_type":    p.cfg.Hetzner.ServerType,
		"location":       p.cfg.Hetzner.Location,
		"flake_ref":      "main",
		"ssh_public_key": p.sshPublicKey,
		"k3s_url":        p.k3sURL,
		"k3s_token":      p.k3sToken,
	}, nil
}

func (p *Provider) ensureKeypair() error {
	if p.wgGenerated {
		return nil
	}
	kp, err := wireguard.GenerateKeypair()
	if err != nil {
		return fmt.Errorf("hetzner: apply: generate wireguard keypair: %w", err)
	}
	p.wgKeypair = kp
	p.wgGenerated = true
	return nil
}

func (p *Provider) sharedDir() string { return filepath.Join(p.workDir, "shared") }

func (p *Provider) EnsureSharedInfra(ctx context.Context) error {
	if p.sshPublicKey == "" {
		return fmt.Errorf("hetzner: ensure shared infra: missing ssh public key")
	}
	tf, err := tfexec.NewTerraform(p.sharedDir(), "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: ensure shared infra: %w", err)
	}
	tf.SetStdout(os.Stderr)
	tf.SetStderr(os.Stderr)
	if err := p.setBaseEnv(tf, nil); err != nil {
		return fmt.Errorf("hetzner: ensure shared infra: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, sharedInfraTimeout)
	defer cancel()
	if err := tf.Init(tctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: ensure shared infra: %w", err)
	}
	if err := tf.Apply(tctx, tfexec.LockTimeout("30s"), tfexec.Var("ssh_public_key="+p.sshPublicKey)); err != nil {
		return fmt.Errorf("hetzner: ensure shared infra: %w", err)
	}
	return nil
}

func (p *Provider) DestroySharedInfra(ctx context.Context) error {
	if p.sshPublicKey == "" {
		return fmt.Errorf("hetzner: destroy shared infra: missing ssh public key")
	}
	tf, err := tfexec.NewTerraform(p.sharedDir(), "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: destroy shared infra: %w", err)
	}
	tf.SetStdout(os.Stderr)
	tf.SetStderr(os.Stderr)
	if err := p.setBaseEnv(tf, nil); err != nil {
		return fmt.Errorf("hetzner: destroy shared infra: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, sharedInfraTimeout)
	defer cancel()
	if err := tf.Init(tctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: destroy shared infra: %w", err)
	}
	if err := tf.Destroy(tctx, tfexec.LockTimeout("30s"), tfexec.Var("ssh_public_key="+p.sshPublicKey)); err != nil {
		return fmt.Errorf("hetzner: destroy shared infra: %w", err)
	}
	return nil
}

func (p *Provider) Apply(ctx context.Context, vars map[string]string) error {
	if err := p.ensureKeypair(); err != nil {
		return err
	}
	if err := p.EnsureSharedInfra(ctx); err != nil {
		return fmt.Errorf("hetzner: apply: %w", err)
	}
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: apply: new terraform: %w", err)
	}
	tf.SetStdout(os.Stderr)
	tf.SetStderr(os.Stderr)
	if err := p.setBaseEnv(tf, map[string]string{
		"HORIZON_WG_PRIVATE_KEY":    p.wgKeypair.PrivateKey,
		"HORIZON_WG_ADDRESS":        p.wgIP + "/32",
		"HORIZON_WG_HUB_PUBLIC_KEY": p.hubPublicKey,
		"HORIZON_K3S_URL":           vars["k3s_url"],
		"HORIZON_K3S_TOKEN":         vars["k3s_token"],
		"HORIZON_SSH_PUBLIC_KEY":    vars["ssh_public_key"],
	}); err != nil {
		return fmt.Errorf("hetzner: apply: set env: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := tf.Init(tctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: apply: init: %w", err)
	}
	if err := p.selectOrCreateWorkspace(tctx, tf); err != nil {
		return err
	}
	applyOpts := []tfexec.ApplyOption{tfexec.LockTimeout("30s")}
	for k, v := range vars {
		applyOpts = append(applyOpts, tfexec.Var(k+"="+v))
	}
	if err := tf.Apply(tctx, applyOpts...); err != nil {
		return fmt.Errorf("hetzner: apply: %w", err)
	}
	outputs, err := tf.Output(tctx)
	if err != nil {
		return fmt.Errorf("hetzner: apply: output: %w", err)
	}
	if out, ok := outputs["server_id"]; ok {
		var id string
		if err := json.Unmarshal(out.Value, &id); err != nil {
			return fmt.Errorf("hetzner: apply: parse server_id: %w", err)
		}
		p.serverID = id
	}
	if out, ok := outputs["server_ipv4"]; ok {
		var ip string
		if err := json.Unmarshal(out.Value, &ip); err != nil {
			return fmt.Errorf("hetzner: apply: parse server_ipv4: %w", err)
		}
		p.serverIP = ip
	}
	return nil
}

func (p *Provider) Destroy(ctx context.Context) error {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: destroy: new terraform: %w", err)
	}
	if err := p.setBaseEnv(tf, nil); err != nil {
		return fmt.Errorf("hetzner: destroy: set env: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := tf.Init(tctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: destroy: init: %w", err)
	}
	wsName := "burst-" + p.burstID
	workspaces, _, err := tf.WorkspaceList(tctx)
	if err != nil {
		return fmt.Errorf("hetzner: destroy: workspace list: %w", err)
	}
	wsExists := false
	for _, w := range workspaces {
		if w == wsName {
			wsExists = true
			break
		}
	}
	if wsExists {
		if err := tf.WorkspaceSelect(tctx, wsName); err != nil {
			return fmt.Errorf("hetzner: destroy: workspace select %s: %w", wsName, err)
		}
	}
	vars, err := p.GenerateTFVars()
	if err != nil {
		return fmt.Errorf("hetzner: destroy: vars: %w", err)
	}
	destroyOpts := []tfexec.DestroyOption{tfexec.LockTimeout("30s")}
	for k, v := range vars {
		destroyOpts = append(destroyOpts, tfexec.Var(k+"="+v))
	}
	if err := tf.Destroy(tctx, destroyOpts...); err != nil {
		return fmt.Errorf("hetzner: destroy: %w", err)
	}
	if wsExists {
		if err := tf.WorkspaceSelect(tctx, "default"); err != nil {
			return fmt.Errorf("hetzner: destroy: workspace select default: %w", err)
		}
		if err := tf.WorkspaceDelete(tctx, wsName); err != nil {
			return fmt.Errorf("hetzner: destroy: workspace delete %s: %w", wsName, err)
		}
	}
	return nil
}

func (p *Provider) Status(ctx context.Context) (string, error) {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return "", fmt.Errorf("hetzner: status: new terraform: %w", err)
	}
	if err := p.setBaseEnv(tf, nil); err != nil {
		return "", fmt.Errorf("hetzner: status: set env: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	outputs, err := tf.Output(tctx)
	if err != nil {
		return "", fmt.Errorf("hetzner: status: output: %w", err)
	}
	out, ok := outputs["server_ipv4"]
	if !ok {
		return "", fmt.Errorf("hetzner: status: output server_ipv4 not found")
	}
	var ip string
	if err := json.Unmarshal(out.Value, &ip); err != nil {
		return "", fmt.Errorf("hetzner: status: parse server_ipv4: %w", err)
	}
	return ip, nil
}

func (p *Provider) BurstID() string { return p.burstID }

func (p *Provider) Hostname() string { return "horizon-burst-" + p.burstID }

func (p *Provider) ServerID() string { return p.serverID }

func (p *Provider) ServerIP() string { return p.serverIP }

func (p *Provider) WireGuardPublicKey() string { return p.wgKeypair.PublicKey }

func (p *Provider) WireGuardIP() string { return p.wgIP }

func (p *Provider) setBaseEnv(tf *tfexec.Terraform, extras map[string]string) error {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		for i := range kv {
			if kv[i] == '=' {
				k, v := kv[:i], kv[i+1:]
				if !strings.HasPrefix(k, "TF_VAR_") {
					env[k] = v
				}
				break
			}
		}
	}
	env["HCLOUD_TOKEN"] = config.Resolve(p.cfg.Hetzner.APITokenEnv, p.cfg.Hetzner.APIToken)
	if path, ok := env["PATH"]; ok {
		env["PATH"] = "/opt/homebrew/bin:" + path
	}
	for k, v := range extras {
		env[k] = v
	}
	return tf.SetEnv(env)
}

func (p *Provider) selectOrCreateWorkspace(ctx context.Context, tf *tfexec.Terraform) error {
	wsName := "burst-" + p.burstID
	workspaces, _, err := tf.WorkspaceList(ctx)
	if err != nil {
		return fmt.Errorf("hetzner: apply: workspace list: %w", err)
	}
	for _, w := range workspaces {
		if w == wsName {
			if err := tf.WorkspaceSelect(ctx, wsName); err != nil {
				return fmt.Errorf("hetzner: apply: workspace select %s: %w", wsName, err)
			}
			return nil
		}
	}
	if err := tf.WorkspaceNew(ctx, wsName); err != nil {
		return fmt.Errorf("hetzner: apply: workspace new %s: %w", wsName, err)
	}
	return nil
}
