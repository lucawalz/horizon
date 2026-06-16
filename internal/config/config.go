package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var ErrNotConfigured = errors.New("horizon is not configured")

type PoolDefaults struct {
	Namespace   string            `mapstructure:"namespace" yaml:"namespace"`
	Cluster     string            `mapstructure:"cluster" yaml:"cluster"`
	DefaultType string            `mapstructure:"default_type" yaml:"default_type"`
	Version     string            `mapstructure:"version" yaml:"version"`
	Types       map[string]string `mapstructure:"types" yaml:"types"`
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

type ClusterDefaults struct {
	Class       string `mapstructure:"class" yaml:"class"`
	WorkerClass string `mapstructure:"worker_class" yaml:"worker_class"`
}

type Config struct {
	RepoPath      string          `mapstructure:"repo_path" yaml:"repo_path"`
	Cluster       string          `mapstructure:"cluster" yaml:"cluster"`
	Kubeconfig    string          `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	Context       string          `mapstructure:"context" yaml:"context"`
	Theme         string          `mapstructure:"theme" yaml:"theme"`
	Pools         PoolDefaults    `mapstructure:"pools" yaml:"pools"`
	ClusterCreate ClusterDefaults `mapstructure:"cluster_create" yaml:"cluster_create"`

	path string
}

func (c *Config) Path() string { return c.path }

const (
	// namespace where the CAPI infra provider's MachineDeployments live; override via pools.namespace per provider
	defaultPoolNamespace = "caph-system"
	defaultPoolCluster   = "burst"
	defaultPoolType      = "reserved"
	defaultPoolVersion   = "v1.35.2+k3s1"
	elasticPoolType      = "elastic"
	reservedPoolType     = "reserved"
	elasticPoolName      = "elastic-workers"
	reservedPoolName     = "reserved-workers"

	ThemeAuto  = "auto"
	ThemeLight = "light"
	ThemeDark  = "dark"
)

func DefaultConfigPath() string {
	var dir string
	switch {
	case os.Getenv("HORIZON_CONFIG_DIR") != "":
		dir = os.Getenv("HORIZON_CONFIG_DIR")
	case os.Getenv("XDG_CONFIG_HOME") != "":
		dir = filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "horizon")
	default:
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "horizon")
	}
	return filepath.Join(dir, "config.yaml")
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(filepath.Dir(DefaultConfigPath()))

	if err := v.ReadInConfig(); err != nil {
		var nf viper.ConfigFileNotFoundError
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("%w: run `horizon init`", ErrNotConfigured)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = v.ConfigFileUsed()

	cfg.RepoPath = os.ExpandEnv(cfg.RepoPath)
	if cfg.RepoPath != "" {
		abs, err := filepath.Abs(cfg.RepoPath)
		if err != nil {
			return nil, fmt.Errorf("repo_path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("repo_path %q: %w", abs, err)
		}
		cfg.RepoPath = abs
	}

	if v.IsSet("infra_path") && cfg.RepoPath == "" {
		return nil, fmt.Errorf("infra_path is retired; set repo_path")
	}

	if v.IsSet("bedrock_path") && v.GetString("bedrock_path") != "" && cfg.RepoPath == "" {
		return nil, fmt.Errorf("bedrock_path is retired; set repo_path")
	}

	applyDefaults(&cfg)
	if err := validateTheme(cfg.Theme); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
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
	if cfg.Theme == "" {
		cfg.Theme = ThemeAuto
	}
}

func Default(path string) *Config {
	cfg := &Config{path: path}
	applyDefaults(cfg)
	return cfg
}

func validateTheme(theme string) error {
	switch theme {
	case ThemeAuto, ThemeLight, ThemeDark:
		return nil
	default:
		return fmt.Errorf("theme %q invalid (want %s|%s|%s)", theme, ThemeLight, ThemeDark, ThemeAuto)
	}
}

func (c *Config) SetTheme(theme string) error {
	if err := validateTheme(theme); err != nil {
		return err
	}
	c.Theme = theme
	return nil
}

func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config path unknown; cannot save")
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("write config %q: %w", c.path, err)
	}
	return nil
}
