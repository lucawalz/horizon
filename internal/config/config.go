package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var ErrNotConfigured = errors.New("horizon is not configured")

type PoolDefaults struct {
	Namespace   string            `mapstructure:"namespace" yaml:"namespace"`
	Cluster     string            `mapstructure:"cluster" yaml:"cluster"`
	DefaultType string            `mapstructure:"default_type" yaml:"default_type"`
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

type SecretRef struct {
	Namespace string `mapstructure:"namespace" yaml:"namespace"`
	Name      string `mapstructure:"name" yaml:"name"`
	Key       string `mapstructure:"key" yaml:"key"`
}

type CredentialSource struct {
	Value  string     `mapstructure:"value" yaml:"value"`
	Path   string     `mapstructure:"path" yaml:"path"`
	Env    string     `mapstructure:"env" yaml:"env"`
	Secret *SecretRef `mapstructure:"secret" yaml:"secret"`
}

func (c CredentialSource) Resolve(ctx context.Context, kc kubernetes.Interface) (string, error) {
	switch {
	case c.Value != "":
		return c.Value, nil
	case c.Path != "":
		data, err := os.ReadFile(ExpandUserPath(c.Path))
		if err != nil {
			return "", fmt.Errorf("read credential file %q: %w", c.Path, err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	case c.Env != "":
		return os.Getenv(c.Env), nil
	case c.Secret != nil:
		return c.Secret.resolve(ctx, kc)
	default:
		return "", fmt.Errorf("credential source empty: set one of value, path, env, or secret")
	}
}

func (s SecretRef) resolve(ctx context.Context, kc kubernetes.Interface) (string, error) {
	if kc == nil {
		return "", fmt.Errorf("secret credential source requires a kubernetes client")
	}
	if s.Namespace == "" || s.Name == "" || s.Key == "" {
		return "", fmt.Errorf("secret credential source requires namespace, name, and key")
	}
	secret, err := kc.CoreV1().Secrets(s.Namespace).Get(ctx, s.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read secret %s/%s: %w", s.Namespace, s.Name, err)
	}
	data, ok := secret.Data[s.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", s.Namespace, s.Name, s.Key)
	}
	return string(data), nil
}

type ReservedImage struct {
	Label string `mapstructure:"label" yaml:"label"`
	Value string `mapstructure:"value" yaml:"value"`
}

type Reserved struct {
	Token      CredentialSource `mapstructure:"token" yaml:"token"`
	CloudInit  CredentialSource `mapstructure:"cloud_init" yaml:"cloud_init"`
	Location   string           `mapstructure:"location" yaml:"location"`
	ServerType string           `mapstructure:"server_type" yaml:"server_type"`
	Image      ReservedImage    `mapstructure:"image" yaml:"image"`
	SSHKeys    []string         `mapstructure:"ssh_keys" yaml:"ssh_keys"`
}

type Config struct {
	Cluster    string       `mapstructure:"cluster" yaml:"cluster"`
	Kubeconfig string       `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	Context    string       `mapstructure:"context" yaml:"context"`
	Pools      PoolDefaults `mapstructure:"pools" yaml:"pools"`
	Reserved   Reserved     `mapstructure:"reserved" yaml:"reserved"`

	path string
}

func (c *Config) Path() string { return c.path }

const (
	defaultPoolType  = "reserved"
	reservedPoolType = "reserved"
	reservedPoolName = "reserved-workers"
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

func ExpandUserPath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(filepath.Dir(DefaultConfigPath()))

	if err := v.ReadInConfig(); err != nil {
		var nf viper.ConfigFileNotFoundError
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("%w: no config file at %s", ErrNotConfigured, DefaultConfigPath())
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = v.ConfigFileUsed()

	applyDefaults(&cfg)

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Pools.DefaultType == "" {
		cfg.Pools.DefaultType = defaultPoolType
	}
	if len(cfg.Pools.Types) == 0 {
		cfg.Pools.Types = map[string]string{
			reservedPoolType: reservedPoolName,
		}
	}
	if cfg.Cluster == "" {
		cfg.Cluster = cfg.Pools.Cluster
	}
}

func Default(path string) *Config {
	cfg := &Config{path: path}
	applyDefaults(cfg)
	return cfg
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
