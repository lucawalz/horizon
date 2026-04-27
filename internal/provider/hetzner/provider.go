package hetzner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

func (p *Provider) Apply(vars map[string]string) error {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: apply: new terraform: %w", err)
	}
	if err := p.setTFEnv(tf, vars); err != nil {
		return fmt.Errorf("hetzner: apply: set env: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := tf.Init(ctx, tfexec.Upgrade(false)); err != nil {
		return fmt.Errorf("hetzner: apply: init: %w", err)
	}
	if err := tf.Apply(ctx, tfexec.LockTimeout("30s")); err != nil {
		return fmt.Errorf("hetzner: apply: %w", err)
	}
	outputs, err := tf.Output(ctx)
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

func (p *Provider) Destroy() error {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return fmt.Errorf("hetzner: destroy: new terraform: %w", err)
	}
	if err := p.setTFEnv(tf, nil); err != nil {
		return fmt.Errorf("hetzner: destroy: set env: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := tf.Destroy(ctx, tfexec.LockTimeout("30s")); err != nil {
		return fmt.Errorf("hetzner: destroy: %w", err)
	}
	return nil
}

func (p *Provider) Status() (string, error) {
	tf, err := tfexec.NewTerraform(p.workDir, "terraform")
	if err != nil {
		return "", fmt.Errorf("hetzner: status: new terraform: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	outputs, err := tf.Output(ctx)
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

func (p *Provider) setTFEnv(tf *tfexec.Terraform, vars map[string]string) error {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		for i := range kv {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	env["HCLOUD_TOKEN"] = os.Getenv(p.cfg.Hetzner.APITokenEnv)
	for k, v := range vars {
		env["TF_VAR_"+k] = v
	}
	return tf.SetEnv(env)
}
