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
	"k8s.io/client-go/util/retry"
)

const (
	burstPhaseConfigMap = "horizon-state"
	burstPhaseNamespace = "kube-system"
	burstPhasesKey      = "burst_phases"
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

func WriteBurstPhase(ctx context.Context, kc kubernetes.Interface, burstID, phase string) error {
	return mutateBurstPhases(ctx, kc, func(phases map[string]string) {
		phases[burstID] = phase
	})
}

func ClearBurstPhase(ctx context.Context, kc kubernetes.Interface, burstID string) error {
	return mutateBurstPhases(ctx, kc, func(phases map[string]string) {
		delete(phases, burstID)
	})
}

func ReadBurstPhases(ctx context.Context, kc kubernetes.Interface) (map[string]string, error) {
	cm, err := kc.CoreV1().ConfigMaps(burstPhaseNamespace).Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("burst-phases: get configmap: %w", err)
	}
	return decodeBurstPhases(cm.Data), nil
}

func decodeBurstPhases(data map[string]string) map[string]string {
	phases := map[string]string{}
	if data == nil {
		return phases
	}
	if v := data[burstPhasesKey]; v != "" {
		_ = json.Unmarshal([]byte(v), &phases)
	}
	return phases
}

func mutateBurstPhases(ctx context.Context, kc kubernetes.Interface, mutate func(map[string]string)) error {
	cms := kc.CoreV1().ConfigMaps(burstPhaseNamespace)
	retriable := func(err error) bool {
		return k8serrors.IsConflict(err) || k8serrors.IsAlreadyExists(err)
	}
	return retry.OnError(retry.DefaultRetry, retriable, func() error {
		cm, err := cms.Get(ctx, burstPhaseConfigMap, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			phases := map[string]string{}
			mutate(phases)
			encoded, mErr := json.Marshal(phases)
			if mErr != nil {
				return fmt.Errorf("burst-phases: marshal: %w", mErr)
			}
			fresh := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: burstPhaseConfigMap, Namespace: burstPhaseNamespace},
				Data:       map[string]string{burstPhasesKey: string(encoded)},
			}
			_, err = cms.Create(ctx, fresh, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return fmt.Errorf("burst-phases: get configmap: %w", err)
		}
		phases := decodeBurstPhases(cm.Data)
		mutate(phases)
		encoded, err := json.Marshal(phases)
		if err != nil {
			return fmt.Errorf("burst-phases: marshal: %w", err)
		}
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[burstPhasesKey] = string(encoded)
		_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
		return err
	})
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
