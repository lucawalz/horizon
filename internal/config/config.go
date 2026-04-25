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
}

type HetznerConfig struct {
	APITokenEnv string `mapstructure:"api_token_env"`
	ServerType  string `mapstructure:"server_type"`
	Location    string `mapstructure:"location"`
}

type AWSConfig struct {
	Region string `mapstructure:"region"`
}

type Config struct {
	Provider   string          `mapstructure:"provider"`
	InfraPath  string          `mapstructure:"infra_path"`
	Kubeconfig string          `mapstructure:"kubeconfig"`
	Thresholds ThresholdConfig `mapstructure:"thresholds"`
	Hetzner    HetznerConfig   `mapstructure:"hetzner"`
	AWS        AWSConfig       `mapstructure:"aws"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	home, _ := os.UserHomeDir()
	v.AddConfigPath(filepath.Join(home, ".config", "horizon"))
	v.AddConfigPath(".")

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
