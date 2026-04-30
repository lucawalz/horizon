package hetzner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/lucawalz/horizon/internal/config"
)

type Provider struct {
	cfg          *config.Config
	workDir      string
	burstID      string
	preAuthKey   string
	sshPublicKey string
	k3sURL       string
	k3sToken     string
	serverID     string
}

func New(cfg *config.Config, workDir string) *Provider {
	return &Provider{cfg: cfg, workDir: workDir, burstID: newBurstID()}
}

func newBurstID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (p *Provider) SetRuntimeSecrets(preAuthKey, sshPublicKey, k3sURL, k3sToken string) {
	p.preAuthKey = preAuthKey
	p.sshPublicKey = sshPublicKey
	p.k3sURL = k3sURL
	p.k3sToken = k3sToken
}

func (p *Provider) GenerateTFVars() (map[string]string, error) {
	if p.preAuthKey == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing headscale preauth key (call SetRuntimeSecrets first)")
	}
	if p.sshPublicKey == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing ssh public key")
	}
	if p.k3sURL == "" || p.k3sToken == "" {
		return nil, fmt.Errorf("hetzner: GenerateTFVars: missing k3s url or token")
	}
	return map[string]string{
		"burst_id":             p.burstID,
		"server_type":          p.cfg.Hetzner.ServerType,
		"location":             p.cfg.Hetzner.Location,
		"flake_ref":            "main",
		"ssh_public_key":       p.sshPublicKey,
		"headscale_preauthkey": p.preAuthKey,
		"headscale_server_url": p.cfg.Headscale.ServerURL,
		"k3s_url":              p.k3sURL,
		"k3s_token":            p.k3sToken,
	}, nil
}

func (p *Provider) Apply(ctx context.Context, vars map[string]string) error {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: apply: new terraform: %w", err)
	}
	tf.SetStdout(os.Stderr)
	tf.SetStderr(os.Stderr)
	if err := p.setBaseEnv(tf, map[string]string{
		"HORIZON_HEADSCALE_PREAUTHKEY": vars["headscale_preauthkey"],
		"HORIZON_HEADSCALE_SERVER_URL": vars["headscale_server_url"],
		"HORIZON_K3S_URL":              vars["k3s_url"],
		"HORIZON_K3S_TOKEN":            vars["k3s_token"],
		"HORIZON_SSH_PUBLIC_KEY":       vars["ssh_public_key"],
	}); err != nil {
		return fmt.Errorf("hetzner: apply: set env: %w", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := tf.Init(tctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: apply: init: %w", err)
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
