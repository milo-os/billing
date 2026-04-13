// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingAccountBindingPhase represents the lifecycle state of a BillingAccountBinding.
// +kubebuilder:validation:Enum=Active;Superseded
type BillingAccountBindingPhase string

const (
	// BillingAccountBindingPhaseActive indicates the binding is the current
	// active binding for the project.
	BillingAccountBindingPhaseActive BillingAccountBindingPhase = "Active"

	// BillingAccountBindingPhaseSuperseded indicates the binding has been
	// replaced by a newer binding.
	BillingAccountBindingPhaseSuperseded BillingAccountBindingPhase = "Superseded"
)

// BillingAccountBindingSpec defines the desired state of a BillingAccountBinding.
// All fields in this spec are immutable once created.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type BillingAccountBindingSpec struct {
	// BillingAccountRef references the billing account to bind.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// ProjectRef references the project to bind to the billing account.
	//
	// +kubebuilder:validation:Required
	ProjectRef ProjectRef `json:"projectRef"`
}

// BillingAccountRef is a reference to a BillingAccount by name within the
// same namespace.
type BillingAccountRef struct {
	// Name is the name of the BillingAccount.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ProjectRef is a reference to a project by name.
type ProjectRef struct {
	// Name is the name of the project.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// BillingResponsibility tracks when billing responsibility was established.
type BillingResponsibility struct {
	// EstablishedAt is the time when billing responsibility was established.
	//
	// +kubebuilder:validation:Optional
	EstablishedAt *metav1.Time `json:"establishedAt,omitempty"`

	// CurrentAccount is the name of the currently responsible billing account.
	//
	// +kubebuilder:validation:Optional
	CurrentAccount string `json:"currentAccount,omitempty"`
}

// BillingAccountBindingStatus defines the observed state of a BillingAccountBinding.
type BillingAccountBindingStatus struct {
	// Phase represents the current lifecycle phase of the binding.
	//
	// +kubebuilder:validation:Optional
	Phase BillingAccountBindingPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the binding's state.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// BillingResponsibility tracks when billing responsibility was established
	// for this binding.
	//
	// +kubebuilder:validation:Optional
	BillingResponsibility *BillingResponsibility `json:"billingResponsibility,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// BillingAccountBinding is the Schema for the billingaccountbindings API.
// It links a project to a billing account, establishing billing responsibility
// for the project's resource consumption.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.projectRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BillingAccountBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingAccountBindingSpec   `json:"spec,omitempty"`
	Status BillingAccountBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingAccountBindingList contains a list of BillingAccountBinding.
type BillingAccountBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingAccountBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingAccountBinding{}, &BillingAccountBindingList{})
}
