package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionAccepted         = "Accepted"
	ConditionInstancesReady   = "InstancesReady"
	ConditionWorkloadMigrated = "WorkloadMigrated"
	ConditionExpired          = "Expired"
	ConditionReleased         = "Released"
	ConditionDegraded         = "Degraded"
)

type LeasePhase string

const (
	LeasePhasePending      LeasePhase = "Pending"
	LeasePhaseProvisioning LeasePhase = "Provisioning"
	LeasePhaseActive       LeasePhase = "Active"
	LeasePhaseExpiring     LeasePhase = "Expiring"
	LeasePhaseReleased     LeasePhase = "Released"
	LeasePhaseDegraded     LeasePhase = "Degraded"
)

type InstancePhase string

const (
	InstancePhaseIntended InstancePhase = "Intended"
	InstancePhaseCreated  InstancePhase = "Created"
	InstancePhaseJoined   InstancePhase = "Joined"
	InstancePhaseDraining InstancePhase = "Draining"
	InstancePhaseReleased InstancePhase = "Released"
)

type WorkloadRef struct {
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

type CapacityLeaseSpec struct {
	// +kubebuilder:validation:MinLength=1
	ProviderRef string `json:"providerRef"`

	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// +kubebuilder:validation:MinLength=1
	Size string `json:"size"`

	// +kubebuilder:validation:XValidation:rule="self >= 1 && self <= 8",message="replicas must be between 1 and 8"
	Replicas int32 `json:"replicas"`

	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5m') && duration(self) <= duration('8h')",message="duration must be between 5m and 8h"
	Duration metav1.Duration `json:"duration"`

	// +optional
	Workload *WorkloadRef `json:"workload,omitempty"`

	// +kubebuilder:default="2m"
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('0s') && duration(self) <= duration('15m')",message="teardownGrace must be between 0s and 15m"
	// +optional
	TeardownGrace *metav1.Duration `json:"teardownGrace,omitempty"`
}

type InstanceStatus struct {
	Name string `json:"name"`

	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// +optional
	NodeName string `json:"nodeName,omitempty"`

	Phase InstancePhase `json:"phase"`

	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// +optional
	LastError string `json:"lastError,omitempty"`
}

type CapacityLeaseStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Phase LeasePhase `json:"phase,omitempty"`

	// +optional
	AcceptedAt *metav1.Time `json:"acceptedAt,omitempty"`

	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// +optional
	WatchdogDeadline *metav1.Time `json:"watchdogDeadline,omitempty"`

	// +optional
	// +listType=atomic
	Instances []InstanceStatus `json:"instances,omitempty"`

	// +optional
	// +listType=atomic
	MigratedWorkloads []string `json:"migratedWorkloads,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=capacityleases,shortName=cl
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Expires",type="date",JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='InstancesReady')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type CapacityLease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CapacityLeaseSpec `json:"spec"`

	// +optional
	Status CapacityLeaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CapacityLeaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []CapacityLease `json:"items"`
}
