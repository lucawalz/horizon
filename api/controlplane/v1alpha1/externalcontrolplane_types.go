package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type APIEndpoint struct {
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
}

type ExternalControlPlaneSpec struct {
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// +optional
	Version string `json:"version,omitempty"`
}

type ControlPlaneInitializationStatus struct {
	// +optional
	ControlPlaneInitialized *bool `json:"controlPlaneInitialized,omitempty"`
}

type ExternalControlPlaneStatus struct {
	// +optional
	Initialization ControlPlaneInitializationStatus `json:"initialization,omitempty"`

	// +optional
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`

	// +optional
	Initialized bool `json:"initialized,omitempty"`

	// +optional
	Ready bool `json:"ready,omitempty"`

	// +optional
	Version string `json:"version,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=externalcontrolplanes,shortName=ecp
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1alpha1"
// +kubebuilder:printcolumn:name="Initialized",type=boolean,JSONPath=`.status.initialized`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.controlPlaneEndpoint.host`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
type ExternalControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalControlPlaneSpec   `json:"spec,omitempty"`
	Status ExternalControlPlaneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ExternalControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalControlPlane `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalControlPlane{}, &ExternalControlPlaneList{})
}
