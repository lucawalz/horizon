package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	burstPhaseConfigMap = "horizon-state"
	burstPhaseNamespace = "kube-system"
	burstPhaseKey       = "burst_phase"
)

const (
	BurstPhaseIdle         = "Idle"
	BurstPhaseBackingUp    = "BackingUp"
	BurstPhaseProvisioning = "Provisioning"
	BurstPhaseJoining      = "Joining"
	BurstPhaseMigrating    = "Migrating"
	BurstPhaseRunning      = "Running"
	BurstPhaseTearingDown  = "TearingDown"
)

func WriteBurstPhase(ctx context.Context, kc kubernetes.Interface, phase string) error {
	cm, err := kc.CoreV1().ConfigMaps(burstPhaseNamespace).Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		fresh := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: burstPhaseConfigMap, Namespace: burstPhaseNamespace},
			Data:       map[string]string{burstPhaseKey: phase},
		}
		_, err = kc.CoreV1().ConfigMaps(burstPhaseNamespace).Create(ctx, fresh, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("burst-phase: get configmap: %w", err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[burstPhaseKey] = phase
	_, err = kc.CoreV1().ConfigMaps(burstPhaseNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func ReadBurstPhase(ctx context.Context, kc kubernetes.Interface) string {
	cm, err := kc.CoreV1().ConfigMaps(burstPhaseNamespace).Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
	if err != nil || cm.Data == nil {
		return BurstPhaseIdle
	}
	if v := cm.Data[burstPhaseKey]; v != "" {
		return v
	}
	return BurstPhaseIdle
}
