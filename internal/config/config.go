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

type PoolDefaults struct {
	Namespace string `mapstructure:"namespace"`
	Cluster   string `mapstructure:"cluster"`
	Name      string `mapstructure:"name"`
}

type Config struct {
	BedrockPath    string          `mapstructure:"bedrock_path"`
	Cluster        string          `mapstructure:"cluster"`
	Kubeconfig     string          `mapstructure:"kubeconfig"`
	Thresholds     ThresholdConfig `mapstructure:"thresholds"`
	Pools          PoolDefaults    `mapstructure:"pools"`
}

const (
	defaultPoolNamespace = "caph-system"
	defaultPoolCluster   = "burst"
	defaultPoolName      = "burst-workers"
)

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	if dir := os.Getenv("HORIZON_CONFIG_DIR"); dir != "" {
		v.AddConfigPath(dir)
	} else {
		home, _ := os.UserHomeDir()
		v.AddConfigPath(filepath.Join(home, ".config", "horizon"))
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.BedrockPath = os.ExpandEnv(cfg.BedrockPath)
	if cfg.BedrockPath != "" {
		abs, err := filepath.Abs(cfg.BedrockPath)
		if err != nil {
			return nil, fmt.Errorf("bedrock_path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("bedrock_path %q: %w", abs, err)
		}
		cfg.BedrockPath = abs
	}

	if v.IsSet("infra_path") && cfg.BedrockPath == "" {
		return nil, fmt.Errorf("infra_path is retired; set bedrock_path")
	}

	if cfg.Pools.Namespace == "" {
		cfg.Pools.Namespace = defaultPoolNamespace
	}
	if cfg.Pools.Cluster == "" {
		cfg.Pools.Cluster = defaultPoolCluster
	}
	if cfg.Pools.Name == "" {
		cfg.Pools.Name = defaultPoolName
	}
	if cfg.Cluster == "" {
		cfg.Cluster = cfg.Pools.Cluster
	}

	return &cfg, nil
}
