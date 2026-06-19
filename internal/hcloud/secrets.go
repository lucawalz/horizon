package hcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const elasticNodeGroup = "elastic"

type SecretRef struct {
	Namespace string
	Name      string
	Key       string
}

type JoinMaterial struct {
	ElasticCloudInit string
	ElasticPoolValue string
}

type clusterConfig struct {
	NodeConfigs map[string]nodeConfig `json:"nodeConfigs"`
}

type nodeConfig struct {
	CloudInit string            `json:"cloudInit"`
	Labels    map[string]string `json:"labels"`
}

func readSecretKey(ctx context.Context, kc kubernetes.Interface, ref SecretRef) ([]byte, error) {
	secret, err := kc.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("hcloud: get secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("hcloud: secret %s/%s missing key %q", ref.Namespace, ref.Name, ref.Key)
	}
	return val, nil
}

func ReadToken(ctx context.Context, kc kubernetes.Interface, ref SecretRef) (string, error) {
	val, err := readSecretKey(ctx, kc, ref)
	if err != nil {
		return "", err
	}
	if len(val) == 0 {
		return "", fmt.Errorf("hcloud: secret %s/%s key %q is empty", ref.Namespace, ref.Name, ref.Key)
	}
	return string(val), nil
}

func ReadJoinMaterial(ctx context.Context, kc kubernetes.Interface, ref SecretRef) (JoinMaterial, error) {
	val, err := readSecretKey(ctx, kc, ref)
	if err != nil {
		return JoinMaterial{}, err
	}
	decoded, err := base64.StdEncoding.DecodeString(string(val))
	if err != nil {
		return JoinMaterial{}, fmt.Errorf("hcloud: decode join config from %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	var cfg clusterConfig
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return JoinMaterial{}, fmt.Errorf("hcloud: parse join config from %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	nc, ok := cfg.NodeConfigs[elasticNodeGroup]
	if !ok {
		return JoinMaterial{}, fmt.Errorf("hcloud: join config from %s/%s missing nodeConfigs.%s", ref.Namespace, ref.Name, elasticNodeGroup)
	}
	if nc.CloudInit == "" {
		return JoinMaterial{}, fmt.Errorf("hcloud: join config from %s/%s has empty cloudInit", ref.Namespace, ref.Name)
	}
	poolValue := nc.Labels[PoolLabelKey]
	if poolValue == "" {
		poolValue = elasticNodeGroup
	}
	return JoinMaterial{ElasticCloudInit: nc.CloudInit, ElasticPoolValue: poolValue}, nil
}
