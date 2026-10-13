package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

//NamespaceClassSpec defines the desired state of NamespaceClass
type NamespaceClassSpec struct {
	// +optional
	Resources []runtime.RawExtension `json:"resources,omitempty"` //resources contains the list of kubernetes manifests to manage in namespaces of this class
}

//now we'll definr a set of resources to be managed in namespaces referencing this class
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=nc
type NamespaceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NamespaceClassSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type NamespaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NamespaceClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NamespaceClass{}, &NamespaceClassList{})
}
