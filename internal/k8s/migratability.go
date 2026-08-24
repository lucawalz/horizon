package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	reasons := t.disruptions
	if t.rolloutReason != "" {
		reasons = append([]string{t.rolloutReason}, reasons...)
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

func deploymentDisruptions(spec appsv1.DeploymentSpec) []string {
	var reasons []string
	if spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		reasons = append(reasons, ReasonRecreateStrategy)
	} else if deploymentReplicas(spec) > 0 && deploymentSurgeCapacity(spec) == 0 {
		reasons = append(reasons, ReasonNoSurgeCapacity)
	}
	return append(reasons, podSpecDisruptions(&spec.Template.Spec)...)
}

func statefulSetDisruptions(spec appsv1.StatefulSetSpec) []string {
	return podSpecDisruptions(&spec.Template.Spec)
}

func podSpecDisruptions(podSpec *corev1.PodSpec) []string {
	if len(podSpec.NodeSelector) > 0 {
		return []string{ReasonNodeSelectorPinned}
	}
	return nil
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
