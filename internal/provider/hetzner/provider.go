package hetzner

import "github.com/lucawalz/horizon/internal/config"

type Provider struct {
	cfg     *config.Config
	workDir string
}

func New(cfg *config.Config, workDir string) *Provider {
	return &Provider{cfg: cfg, workDir: workDir}
}
