package catalogue

import (
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/testenv"
)

var testEnv *testenv.Environment

func TestMain(m *testing.M) {
	env, err := testenv.Start(clusterScheme())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testEnv = env

	code := m.Run()

	if err := env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
	}
	os.Exit(code)
}

func clusterScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func acceptedProviderConfig(name string, maxLifetime time.Duration) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ProviderConfigSpec{
			Type: v1alpha1.ProviderTypeHetzner,
			Hetzner: &v1alpha1.HetznerProviderSpec{
				CredentialsSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "hcloud"},
					Key:                  "token",
				},
				CloudInitSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cloud-init"},
					Key:                  "user-data",
				},
				Image: &v1alpha1.ImageSpec{Name: "bedrock-cluster-node"},
			},
			Watchdog: v1alpha1.WatchdogPolicy{
				RenewInterval: metav1.Duration{Duration: time.Minute},
				Slack:         metav1.Duration{Duration: 2 * time.Minute},
				MaxLifetime:   metav1.Duration{Duration: maxLifetime},
			},
		},
	}
}
