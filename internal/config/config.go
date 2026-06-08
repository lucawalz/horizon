package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type ThresholdConfig struct {
	Burst           float64 `mapstructure:"burst"`
	ScaleDown       float64 `mapstructure:"scale_down"`
	Window          int     `mapstructure:"window"`
	CooldownMinutes int     `mapstructure:"cooldown_minutes"`
	MaxBurstNodes   int     `mapstructure:"max_burst_nodes"`
}

type HetznerConfig struct {
	APIToken    string `mapstructure:"api_token"`
	APITokenEnv string `mapstructure:"api_token_env"`
	ServerType  string `mapstructure:"server_type"`
	Location    string `mapstructure:"location"`
}

type ZeroTierConfig struct {
	NetworkID   string `mapstructure:"network_id"`
	APITokenEnv string `mapstructure:"api_token_env"`
	APIToken    string `mapstructure:"api_token"`
	MasterIP    string `mapstructure:"master_ip"`
}

type AWSConfig struct {
	Region string `mapstructure:"region"`
}

type K3sConfig struct {
	URL         string `mapstructure:"url"`
	URLEnv      string `mapstructure:"url_env"`
	Token       string `mapstructure:"token"`
	TokenEnv    string `mapstructure:"token_env"`
	SSHPublicKey string `mapstructure:"ssh_public_key"`
	SSHKeyEnv   string `mapstructure:"ssh_public_key_env"`
}

func Resolve(envName, inline string) string {
	if inline != "" {
		return inline
	}
	return os.Getenv(envName)
}

type Config struct {
	Provider       string          `mapstructure:"provider"`
	InfraPath      string          `mapstructure:"infra_path"`
	Kubeconfig     string          `mapstructure:"kubeconfig"`
	Thresholds     ThresholdConfig `mapstructure:"thresholds"`
	Hetzner        HetznerConfig   `mapstructure:"hetzner"`
	ZeroTier       ZeroTierConfig  `mapstructure:"zerotier"`
	K3s            K3sConfig       `mapstructure:"k3s"`
	AWS            AWSConfig       `mapstructure:"aws"`
	PushgatewayURL string          `mapstructure:"pushgateway_url"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	home, _ := os.UserHomeDir()
	v.AddConfigPath(".")
	v.AddConfigPath(filepath.Join(home, ".config", "horizon"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.InfraPath = os.ExpandEnv(cfg.InfraPath)
	if cfg.InfraPath != "" {
		abs, err := filepath.Abs(cfg.InfraPath)
		if err != nil {
			return nil, fmt.Errorf("infra_path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("infra_path %q: %w", abs, err)
		}
		cfg.InfraPath = abs
	}

	return &cfg, nil
}
