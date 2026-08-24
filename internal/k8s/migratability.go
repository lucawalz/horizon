package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

type MigrationVerdict string

const (
	VerdictSeamless   MigrationVerdict = "Seamless"
	VerdictDisruptive MigrationVerdict = "Disruptive"
)

const (
	ReasonRolloutPaused      = "RolloutPaused"
	ReasonManualRollout      = "ManualRollout"
	ReasonPartitionedRollout = "PartitionedRollout"
	ReasonRecreateStrategy   = "RecreateStrategy"
	ReasonNoSurgeCapacity    = "NoSurgeCapacity"
	ReasonNodeSelectorPinned = "NodeSelectorPinned"
)

const (
	defaultReplicas           = 1
	defaultDeploymentMaxSurge = "25%"
)

type WorkloadMigratability struct {
	Workload string
	Verdict  MigrationVerdict
	Reasons  []string
}

func ClassifyMigratability(ctx context.Context, kc kubernetes.Interface, namespace string) ([]WorkloadMigratability, error) {
	if err := ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}

	var assessments []WorkloadMigratability
	for _, wc := range workloadClients(kc, namespace) {
		targets, err := wc.list(ctx)
		if err != nil {
			return nil, fmt.Errorf("classify: list %s in %q: %w", wc.plural(), wc.namespace, err)
		}
		for _, t := range targets {
			assessment, err := t.migratability(wc.kind)
			if err != nil {
				return nil, err
			}
			assessments = append(assessments, assessment)
		}
	}
	return assessments, nil
}

func (t workloadTarget) migratability(kind string) (WorkloadMigratability, error) {
	ref := workloadRef(kind, t.name)
	if t.replicas == 0 {
		return WorkloadMigratability{Workload: ref, Verdict: VerdictSeamless}, nil
	}

	pinned, err := t.preBurstNodeSelector(kind)
	if err != nil {
		return WorkloadMigratability{}, err
	}

	var reasons []string
	if t.rolloutReason != "" {
		reasons = append(reasons, t.rolloutReason)
	}
	if t.strategyReason != "" {
		reasons = append(reasons, t.strategyReason)
	}
	if len(pinned) > 0 {
		reasons = append(reasons, ReasonNodeSelectorPinned)
	}

	verdict := VerdictSeamless
	if len(reasons) > 0 {
		verdict = VerdictDisruptive
	}
	return WorkloadMigratability{Workload: ref, Verdict: verdict, Reasons: reasons}, nil
}

func deploymentRolloutReason(spec appsv1.DeploymentSpec) string {
	if spec.Paused {
		return ReasonRolloutPaused
	}
	return ""
}

func statefulSetRolloutReason(strategy appsv1.StatefulSetUpdateStrategy) string {
	if strategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
		return ReasonManualRollout
	}
	rollingUpdate := strategy.RollingUpdate
	if rollingUpdate != nil && rollingUpdate.Partition != nil && *rollingUpdate.Partition != 0 {
		return ReasonPartitionedRollout
	}
	return ""
}

func (t workloadTarget) preBurstNodeSelector(kind string) (map[string]string, error) {
	placement, ok := t.annotations[PrePlacementAnnotationKey]
	if !ok {
		return t.podSpec.NodeSelector, nil
	}
	// migration empties the live node selector, so a workload already on burst shows its pin only in the saved placement
	var saved savedPlacement
	if err := json.Unmarshal([]byte(placement), &saved); err != nil {
		return nil, fmt.Errorf("classify: unmarshal placement for %s %q: %w", kind, t.name, err)
	}
	return saved.NodeSelector, nil
}

func deploymentStrategyReason(spec appsv1.DeploymentSpec) string {
	if spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		return ReasonRecreateStrategy
	}
	if deploymentSurgeCapacity(spec) == 0 {
		return ReasonNoSurgeCapacity
	}
	return ""
}

func desiredReplicas(replicas *int32) int {
	if replicas == nil {
		return defaultReplicas
	}
	return int(*replicas)
}

func deploymentSurgeCapacity(spec appsv1.DeploymentSpec) int {
	maxSurge := intstr.FromString(defaultDeploymentMaxSurge)
	if rollingUpdate := spec.Strategy.RollingUpdate; rollingUpdate != nil && rollingUpdate.MaxSurge != nil {
		maxSurge = *rollingUpdate.MaxSurge
	}
	surge, err := intstr.GetScaledValueFromIntOrPercent(&maxSurge, desiredReplicas(spec.Replicas), true)
	if err != nil {
		return 0
	}
	return surge
}
