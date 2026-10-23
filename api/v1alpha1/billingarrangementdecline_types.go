// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingArrangementDeclineSpec defines the desired state of a
// BillingArrangementDecline.
type BillingArrangementDeclineSpec struct {
	// ApplicationRef references the BillingArrangementApplication in the
	// same namespace that is declined. Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="applicationRef is immutable"
	ApplicationRef BillingArrangementApplicationRef `json:"applicationRef"`

	// Message is shown to the applicant, and copied onto the
	// application's status.declineMessage. Tell them what to do next, for
	// example "Reply to our email with your company registration number,
	// or add a card to continue."
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2000
	Message string `json:"message"`

	// InternalNotes record the reviewer's reasoning. Internal: never shown
	// to customers.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=4000
	InternalNotes string `json:"internalNotes,omitempty"`
}

// BillingArrangementApplicationRef references a
// BillingArrangementApplication in the same namespace.
type BillingArrangementApplicationRef struct {
	// Name is the name of the BillingArrangementApplication.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// BillingArrangementDecline is the Schema for the
// billingarrangementdeclines API.
//
// A BillingArrangementDecline records staff declining a payment terms
// application. It's a separate resource so the reviewer's internal
// notes stay out of reach of customers, who can read their applications
// but never declines; the customer-facing message is copied onto the
// application's status. Each decline is owned by its application.
//
// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Application",type=string,JSONPath=`.spec.applicationRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type BillingArrangementDecline struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec BillingArrangementDeclineSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// BillingArrangementDeclineList contains a list of
// BillingArrangementDecline.
type BillingArrangementDeclineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingArrangementDecline `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingArrangementDecline{}, &BillingArrangementDeclineList{})
}
