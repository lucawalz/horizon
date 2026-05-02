package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

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

const (
	watchPressureCountKey  = "pressure_count"
	watchCooldownUntilKey  = "cooldown_until"
	watchActiveBurstIDsKey = "active_burst_ids"
)

type WatchState struct {
	PressureCount  int
	CooldownUntil  time.Time
	ActiveBurstIDs []string
}

func ReadWatchState(ctx context.Context, kc kubernetes.Interface) (WatchState, error) {
	cm, err := kc.CoreV1().ConfigMaps(burstPhaseNamespace).Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return WatchState{}, nil
	}
	if err != nil {
		return WatchState{}, fmt.Errorf("watch-state: get configmap: %w", err)
	}
	var ws WatchState
	if cm.Data == nil {
		return ws, nil
	}
	if v := cm.Data[watchPressureCountKey]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ws.PressureCount = n
		}
	}
	if v := cm.Data[watchCooldownUntilKey]; v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			ws.CooldownUntil = t
		}
	}
	if v := cm.Data[watchActiveBurstIDsKey]; v != "" {
		var ids []string
		if err := json.Unmarshal([]byte(v), &ids); err == nil {
			ws.ActiveBurstIDs = ids
		}
	}
	return ws, nil
}

func WriteWatchState(ctx context.Context, kc kubernetes.Interface, ws WatchState) error {
	idsJSON, err := json.Marshal(ws.ActiveBurstIDs)
	if err != nil {
		return fmt.Errorf("watch-state: marshal active_burst_ids: %w", err)
	}
	patches := map[string]string{
		watchPressureCountKey:  strconv.Itoa(ws.PressureCount),
		watchCooldownUntilKey:  ws.CooldownUntil.UTC().Format(time.RFC3339),
		watchActiveBurstIDsKey: string(idsJSON),
	}
	cm, err := kc.CoreV1().ConfigMaps(burstPhaseNamespace).Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		fresh := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: burstPhaseConfigMap, Namespace: burstPhaseNamespace},
			Data:       patches,
		}
		_, err = kc.CoreV1().ConfigMaps(burstPhaseNamespace).Create(ctx, fresh, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("watch-state: get configmap: %w", err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	for k, v := range patches {
		cm.Data[k] = v
	}
	_, err = kc.CoreV1().ConfigMaps(burstPhaseNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}
