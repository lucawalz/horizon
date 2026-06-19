package hcloud_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	hz "github.com/lucawalz/horizon/internal/hcloud"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const clusterConfigJSON = `{"nodeConfigs":{"elastic":{"cloudInit":"#cloud-config\nnode-label:\n- horizon.dev/pool=elastic\n","labels":{"horizon.dev/pool":"elastic"}}}}`

func tokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hcloud", Namespace: "kube-system"},
		Data:       map[string][]byte{"hcloud-token": []byte("tok123")},
	}
}

func joinSecret() *corev1.Secret {
	inner := base64.StdEncoding.EncodeToString([]byte(clusterConfigJSON))
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-hcloud-config", Namespace: "kube-system"},
		Data:       map[string][]byte{"HCLOUD_CLUSTER_CONFIG": []byte(inner)},
	}
}

func tokenRef() hz.SecretRef {
	return hz.SecretRef{Namespace: "kube-system", Name: "hcloud", Key: "hcloud-token"}
}

func joinRef() hz.SecretRef {
	return hz.SecretRef{Namespace: "kube-system", Name: "cluster-autoscaler-hcloud-config", Key: "HCLOUD_CLUSTER_CONFIG"}
}

func TestReadToken(t *testing.T) {
	kc := fake.NewSimpleClientset(tokenSecret())
	tok, err := hz.ReadToken(context.Background(), kc, tokenRef())
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if tok != "tok123" {
		t.Errorf("token = %q, want tok123", tok)
	}
}

func TestReadTokenFailsFastWhenMissing(t *testing.T) {
	kc := fake.NewSimpleClientset()
	_, err := hz.ReadToken(context.Background(), kc, tokenRef())
	if err == nil || !strings.Contains(err.Error(), "hcloud") {
		t.Fatalf("expected precise missing-secret error, got %v", err)
	}
}

func TestReadJoinMaterialDecodesAndExtractsElastic(t *testing.T) {
	kc := fake.NewSimpleClientset(joinSecret())
	jm, err := hz.ReadJoinMaterial(context.Background(), kc, joinRef())
	if err != nil {
		t.Fatalf("ReadJoinMaterial: %v", err)
	}
	if jm.ElasticPoolValue != "elastic" {
		t.Errorf("pool value = %q, want elastic", jm.ElasticPoolValue)
	}
	if !strings.Contains(jm.ElasticCloudInit, "horizon.dev/pool=elastic") {
		t.Errorf("cloud-init missing elastic label: %q", jm.ElasticCloudInit)
	}
}

func TestReadJoinMaterialFailsFastWhenKeyMissing(t *testing.T) {
	s := joinSecret()
	delete(s.Data, "HCLOUD_CLUSTER_CONFIG")
	kc := fake.NewSimpleClientset(s)
	_, err := hz.ReadJoinMaterial(context.Background(), kc, joinRef())
	if err == nil || !strings.Contains(err.Error(), "missing key") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}
