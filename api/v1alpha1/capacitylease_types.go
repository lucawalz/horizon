package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionAccepted           = "Accepted"
	ConditionInstancesReady     = "InstancesReady"
	ConditionWatchdogArmed      = "WatchdogArmed"
	ConditionWorkloadMigratable = "WorkloadMigratable"
	ConditionWorkloadMigrated   = "WorkloadMigrated"
	ConditionWorkloadReplicable = "WorkloadReplicable"
	ConditionExpiryClamped      = "ExpiryClamped"
	ConditionExpired            = "Expired"
	ConditionReleased           = "Released"
	ConditionDegraded           = "Degraded"
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

type InstanceStage string

const (
	InstanceStageAwaitingInstance     InstanceStage = "AwaitingInstance"
	InstanceStageAwaitingRegistration InstanceStage = "AwaitingRegistration"
	InstanceStageAwaitingReady        InstanceStage = "AwaitingReady"
	InstanceStageReady                InstanceStage = "Ready"
)

type WorkloadMode string

const (
	WorkloadModeMove      WorkloadMode = "move"
	WorkloadModeReplicate WorkloadMode = "replicate"
)

// +kubebuilder:validation:XValidation:rule="(has(self.mode) && self.mode == 'replicate') == has(self.burstReplicas)",message="burstReplicas belongs to replicate mode and is required by it"
type WorkloadRef struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +listType=set
	Namespaces []string `json:"namespaces"`

	// the selector names workloads inside the target namespaces rather than the namespaces themselves, and its absence names every one of them
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// move repins each matched workload onto the leased nodes and restores it at expiry; replicate never writes to the matched workload and runs a lease-owned copy of it on the leased nodes instead
	// +kubebuilder:default=move
	// +kubebuilder:validation:Enum=move;replicate
	// +optional
	Mode WorkloadMode `json:"mode,omitempty"`

	// the number of pods each replicated copy runs, a count of pods rather than the machines spec.replicas names
	// +kubebuilder:validation:Minimum=1
	// +optional
	BurstReplicas *int32 `json:"burstReplicas,omitempty"`
}

type Architecture string

const (
	ArchitectureX86 Architecture = "x86"
	ArchitectureARM Architecture = "arm"
)

type CPUType string

const (
	CPUTypeShared    CPUType = "shared"
	CPUTypeDedicated CPUType = "dedicated"
)

type SizingStrategy string

const (
	StrategyLowestPrice        SizingStrategy = "LowestPrice"
	StrategyLowestPricePerCore SizingStrategy = "LowestPricePerCore"
)

type SizeRequirements struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	MinCPU int32 `json:"minCPU"`

	// +optional
	MinMemory *resource.Quantity `json:"minMemory,omitempty"`

	// the image an instance boots is resolved from its architecture, so a candidate set cannot be built without one
	// +kubebuilder:validation:Enum=x86;arm
	Architecture Architecture `json:"architecture"`

	// +kubebuilder:validation:Enum=shared;dedicated
	// +optional
	CPUType CPUType `json:"cpuType,omitempty"`

	// +kubebuilder:default=LowestPrice
	// +kubebuilder:validation:Enum=LowestPrice;LowestPricePerCore
	// +optional
	Strategy SizingStrategy `json:"strategy,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.size) != has(self.requirements)",message="set exactly one of size or requirements"
// +kubebuilder:validation:XValidation:rule="self.providerRef == oldSelf.providerRef",message="providerRef is immutable"
// +kubebuilder:validation:XValidation:rule="self.region == oldSelf.region",message="region is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.size) == has(oldSelf.size) && (!has(self.size) || self.size == oldSelf.size)",message="size is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.requirements) == has(oldSelf.requirements) && (!has(self.requirements) || self.requirements == oldSelf.requirements)",message="requirements are immutable"
type CapacityLeaseSpec struct {
	// +kubebuilder:validation:MinLength=1
	ProviderRef string `json:"providerRef"`

	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// +kubebuilder:validation:MinLength=1
	// +optional
	Size string `json:"size,omitempty"`

	// +optional
	Requirements *SizeRequirements `json:"requirements,omitempty"`

	// the number of machines the lease rents, a count of machines rather than the pods spec.workload.burstReplicas names
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

	// +kubebuilder:validation:Enum=AwaitingInstance;AwaitingRegistration;AwaitingReady;Ready
	// +optional
	Stage InstanceStage `json:"stage,omitempty"`

	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// +optional
	BackstopAt *metav1.Time `json:"backstopAt,omitempty"`

	// +optional
	LastError string `json:"lastError,omitempty"`
}

type RejectedCandidates struct {
	Reason string `json:"reason"`
	Count  int32  `json:"count"`
}

type MigrationWarning struct {
	Workload string `json:"workload"`

	// +listType=atomic
	Reasons []string `json:"reasons"`
}

type SelectionStatus struct {
	Strategy SizingStrategy `json:"strategy"`

	Chosen string `json:"chosen"`

	// +optional
	HourlyRate string `json:"hourlyRate,omitempty"`

	// +optional
	Currency string `json:"currency,omitempty"`

	// +optional
	RunnerUp string `json:"runnerUp,omitempty"`

	Offered int32 `json:"offered"`

	Qualified int32 `json:"qualified"`

	// +optional
	// +listType=map
	// +listMapKey=reason
	Rejected []RejectedCandidates `json:"rejected,omitempty"`

	DecidedAt metav1.Time `json:"decidedAt"`
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
	ProviderConfig string `json:"providerConfig,omitempty"`

	// +optional
	Region string `json:"region,omitempty"`

	// +optional
	InstanceType string `json:"instanceType,omitempty"`

	// +optional
	Selection *SelectionStatus `json:"selection,omitempty"`

	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`

	// +optional
	ReleasedAt *metav1.Time `json:"releasedAt,omitempty"`

	// +optional
	// +listType=atomic
	Instances []InstanceStatus `json:"instances,omitempty"`

	// +optional
	// +listType=atomic
	MigratedWorkloads []string `json:"migratedWorkloads,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=workload
	MigrationWarnings []MigrationWarning `json:"migrationWarnings,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

func (s *CapacityLeaseStatus) LifetimeBackstop() *metav1.Time {
	// a released machine enforces nothing any more, so the deadline it once capped is no longer a bound on this lease
	var earliest *metav1.Time
	for i := range s.Instances {
		entry := &s.Instances[i]
		if entry.Phase == InstancePhaseReleased || entry.BackstopAt.IsZero() {
			continue
		}
		if earliest == nil || entry.BackstopAt.Time.Before(earliest.Time) {
			earliest = entry.BackstopAt
		}
	}
	return earliest.DeepCopy()
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=capacityleases,shortName=cl
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Expires",type="string",JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='InstancesReady')].status"
// +kubebuilder:printcolumn:name="Armed",type="string",JSONPath=".status.conditions[?(@.type=='WatchdogArmed')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".status.instanceType"
// +kubebuilder:printcolumn:name="ReadyAt",type="date",JSONPath=".status.readyAt",priority=1
// +kubebuilder:printcolumn:name="ReleasedAt",type="date",JSONPath=".status.releasedAt",priority=1
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
