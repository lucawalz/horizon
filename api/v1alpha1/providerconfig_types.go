package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ConditionReady = "Ready"

const ProviderTypeHetzner = "hetzner"

type HetznerProviderSpec struct {
	CredentialsSecretRef corev1.SecretKeySelector `json:"credentialsSecretRef"`

	// +optional
	NodeCredentialSecretRef *corev1.SecretKeySelector `json:"nodeCredentialSecretRef,omitempty"`

	// +optional
	ImageSelector map[string]string `json:"imageSelector,omitempty"`

	// +optional
	// +listType=atomic
	SSHKeys []string `json:"sshKeys,omitempty"`

	CloudInitSecretRef corev1.SecretKeySelector `json:"cloudInitSecretRef"`
}

type WatchdogPolicy struct {
	RenewInterval metav1.Duration `json:"renewInterval"`

	Slack metav1.Duration `json:"slack"`

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
