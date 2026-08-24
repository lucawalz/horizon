// Package v1alpha1 contains the horizon.dev API types for leased burst capacity.
// +kubebuilder:object:generate=true
// +groupName=horizon.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	GroupVersion = schema.GroupVersion{Group: "horizon.dev", Version: "v1alpha1"}

	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(
		GroupVersion,
		&CapacityLease{},
		&CapacityLeaseList{},
		&ProviderConfig{},
		&ProviderConfigList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
