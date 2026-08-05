package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ConditionReady = "Ready"

const ProviderTypeHetzner = "hetzner"

// +kubebuilder:validation:XValidation:rule="[has(self.name), has(self.id), has(self.selector)].filter(x, x).size() == 1",message="set exactly one of name, id or selector"
type ImageSpec struct {
	// +optional
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	ID int64 `json:"id,omitempty"`

	// +optional
	// +kubebuilder:validation:MinProperties=1
	Selector map[string]string `json:"selector,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.image) != has(self.imageSelector)",message="set exactly one of image or the deprecated imageSelector"
type HetznerProviderSpec struct {
	CredentialsSecretRef corev1.SecretKeySelector `json:"credentialsSecretRef"`

	// +optional
	NodeCredentialSecretRef *corev1.SecretKeySelector `json:"nodeCredentialSecretRef,omitempty"`

	// +optional
	JoinTokenSecretRef *corev1.SecretKeySelector `json:"joinTokenSecretRef,omitempty"`

	// +optional
	// +kubebuilder:validation:MinProperties=1
	ImageSelector map[string]string `json:"imageSelector,omitempty"`

	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// +optional
	// +listType=atomic
	SSHKeys []string `json:"sshKeys,omitempty"`

	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5
	// +kubebuilder:validation:items:MinLength=1
	Firewalls []string `json:"firewalls,omitempty"`

	CloudInitSecretRef corev1.SecretKeySelector `json:"cloudInitSecretRef"`
}

// +kubebuilder:validation:XValidation:rule="duration(self.slack) > duration(self.renewInterval)",message="slack must be greater than renewInterval"
// +kubebuilder:validation:XValidation:rule="duration(self.renewInterval) + duration(self.slack) <= duration('1h')",message="renewInterval plus slack must not exceed 1h"
// +kubebuilder:validation:XValidation:rule="duration(self.maxLifetime) > duration(self.renewInterval) + duration(self.slack)",message="maxLifetime must be greater than renewInterval plus slack"
type WatchdogPolicy struct {
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('10s')",message="renewInterval must be at least 10s"
	RenewInterval metav1.Duration `json:"renewInterval"`

	Slack metav1.Duration `json:"slack"`

	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5m') && duration(self) <= duration('24h')",message="maxLifetime must be between 5m and 24h"
	MaxLifetime metav1.Duration `json:"maxLifetime"`
}

// +kubebuilder:validation:XValidation:rule="(self.type == 'hetzner') == has(self.hetzner)",message="exactly one provider block must be set and it must match type"
type ProviderConfigSpec struct {
	// +kubebuilder:validation:XValidation:rule="self == 'hetzner'",message="type must be one of: hetzner"
	Type string `json:"type"`

	// +optional
	Hetzner *HetznerProviderSpec `json:"hetzner,omitempty"`

	Watchdog WatchdogPolicy `json:"watchdog"`
}

type ProviderConfigStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=providerconfigs
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type ProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ProviderConfigSpec `json:"spec"`

	// +optional
	Status ProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ProviderConfig `json:"items"`
}
