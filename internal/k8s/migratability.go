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
	defaultDeploymentReplicas = 1
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
			assessments = append(assessments, t.migratability(wc.kind))
		}
	}
	return assessments, nil
}

func (t workloadTarget) migratability(kind string) WorkloadMigratability {
	var reasons []string
	if t.rolloutReason != "" {
		reasons = append(reasons, t.rolloutReason)
	}
	if t.strategyReason != "" {
		reasons = append(reasons, t.strategyReason)
	}
	if len(t.preBurstNodeSelector()) > 0 {
		reasons = append(reasons, ReasonNodeSelectorPinned)
	}

	verdict := VerdictSeamless
	if len(reasons) > 0 {
		verdict = VerdictDisruptive
	}
	return WorkloadMigratability{Workload: workloadRef(kind, t.name), Verdict: verdict, Reasons: reasons}
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

func (t workloadTarget) preBurstNodeSelector() map[string]string {
	// migration clears the node selector, so a repeated pass has to read the pin back out of the saved placement
	placement, ok := t.annotations[PrePlacementAnnotationKey]
	if !ok {
		return t.podSpec.NodeSelector
	}
	var saved savedPlacement
	if err := json.Unmarshal([]byte(placement), &saved); err != nil {
		return t.podSpec.NodeSelector
	}
	return saved.NodeSelector
}

func deploymentStrategyReason(spec appsv1.DeploymentSpec) string {
	if spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		return ReasonRecreateStrategy
	}
	if deploymentReplicas(spec) > 0 && deploymentSurgeCapacity(spec) == 0 {
		return ReasonNoSurgeCapacity
	}
	return ""
}

func deploymentReplicas(spec appsv1.DeploymentSpec) int {
	if spec.Replicas == nil {
		return defaultDeploymentReplicas
	}
	return int(*spec.Replicas)
}

func deploymentSurgeCapacity(spec appsv1.DeploymentSpec) int {
	maxSurge := intstr.FromString(defaultDeploymentMaxSurge)
	if rollingUpdate := spec.Strategy.RollingUpdate; rollingUpdate != nil && rollingUpdate.MaxSurge != nil {
		maxSurge = *rollingUpdate.MaxSurge
	}
	surge, err := intstr.GetScaledValueFromIntOrPercent(&maxSurge, deploymentReplicas(spec), true)
	if err != nil {
		return 0
	}
	return surge
}
