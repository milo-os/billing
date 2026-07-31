// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingEntitlementConditionOfferAssigned is True when the referenced
// Offer exists and is in the GA launch stage.
const BillingEntitlementConditionOfferAssigned = "OfferAssigned"

// BillingEntitlementSpec defines the desired state of a BillingEntitlement.
//
// Exactly one active BillingEntitlement exists per billing account. It
// binds the account to a published Offer. Distinct from the per-project
// ServiceEntitlement in service-catalog.
type BillingEntitlementSpec struct {
	// BillingAccountRef references the BillingAccount in the same
	// namespace.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// OfferRef references a cluster-scoped published (GA) Offer.
	//
	// +kubebuilder:validation:Required
	OfferRef OfferReference `json:"offerRef"`
}

// BillingEntitlementStatus defines the observed state of a BillingEntitlement.
type BillingEntitlementStatus struct {
	// Conditions represent the latest available observations of the
	// entitlement's state.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// BillingEntitlement is the Schema for the billingentitlements API.
// Billing-account-scoped binding of an account to exactly one active Offer.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="BillingAccount",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Offer",type=string,JSONPath=`.spec.offerRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +genclient
type BillingEntitlement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingEntitlementSpec   `json:"spec,omitempty"`
	Status BillingEntitlementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingEntitlementList contains a list of BillingEntitlement.
type BillingEntitlementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingEntitlement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingEntitlement{}, &BillingEntitlementList{})
}
