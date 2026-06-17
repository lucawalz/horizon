// +kubebuilder:object:generate=true
// +groupName=controlplane.horizon.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion = schema.GroupVersion{Group: "controlplane.horizon.dev", Version: "v1alpha1"}

	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019: standard controller-runtime api registration pattern

	AddToScheme = SchemeBuilder.AddToScheme
)
