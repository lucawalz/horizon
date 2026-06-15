package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	Namespace   string            `mapstructure:"namespace"`
	Cluster     string            `mapstructure:"cluster"`
	DefaultType string            `mapstructure:"default_type"`
	Version     string            `mapstructure:"version"`
	Types       map[string]string `mapstructure:"types"`
}

func (p PoolDefaults) Resolve(typeName string) (string, error) {
	if typeName == "" {
		typeName = p.DefaultType
	}
	if md, ok := p.Types[typeName]; ok {
		return md, nil
	}
	known := make([]string, 0, len(p.Types))
	for t := range p.Types {
		known = append(known, t)
	}
	sort.Strings(known)
	return "", fmt.Errorf("unknown pool type %q (known: %s)", typeName, strings.Join(known, ", "))
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
	defaultPoolType      = "reserved"
	defaultPoolVersion   = "v1.35.2+k3s1"
	elasticPoolType      = "elastic"
	reservedPoolType     = "reserved"
	elasticPoolName      = "elastic-workers"
	reservedPoolName     = "reserved-workers"
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
	if cfg.Pools.DefaultType == "" {
		cfg.Pools.DefaultType = defaultPoolType
	}
	if cfg.Pools.Version == "" {
		cfg.Pools.Version = defaultPoolVersion
	}
	if len(cfg.Pools.Types) == 0 {
		cfg.Pools.Types = map[string]string{
			elasticPoolType:  elasticPoolName,
			reservedPoolType: reservedPoolName,
		}
	}
	if cfg.Cluster == "" {
		cfg.Cluster = cfg.Pools.Cluster
	}

	return &cfg, nil
}
