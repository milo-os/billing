// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingAccountPhase represents the lifecycle state of a BillingAccount.
// +kubebuilder:validation:Enum=Provisioning;Ready;Suspended;Archived
type BillingAccountPhase string

const (
	// BillingAccountPhaseProvisioning indicates the account is being set up.
	BillingAccountPhaseProvisioning BillingAccountPhase = "Provisioning"

	// BillingAccountPhaseReady indicates the account is active and can accept bindings.
	BillingAccountPhaseReady BillingAccountPhase = "Ready"

	// BillingAccountPhaseSuspended indicates the account has been suspended.
	BillingAccountPhaseSuspended BillingAccountPhase = "Suspended"

	// BillingAccountPhaseArchived indicates the account has been closed and is read-only.
	BillingAccountPhaseArchived BillingAccountPhase = "Archived"
)

// BillingAccountSpec defines the desired state of a BillingAccount.
type BillingAccountSpec struct {
	// CurrencyCode is the ISO 4217 currency code for this billing account.
	// This field is immutable once the account transitions past Provisioning phase.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Z]{3}$`
	CurrencyCode string `json:"currencyCode"`

	// PaymentTerms defines the invoicing schedule for this billing account.
	//
	// +kubebuilder:validation:Optional
	PaymentTerms *PaymentTerms `json:"paymentTerms,omitempty"`

	// ContactInfo defines the billing contact for notifications.
	//
	// +kubebuilder:validation:Optional
	ContactInfo *BillingContactInfo `json:"contactInfo,omitempty"`
}

// PaymentTerms defines the payment schedule for a billing account.
type PaymentTerms struct {
	// NetDays is the number of days after invoice date that payment is due.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=30
	NetDays int32 `json:"netDays,omitempty"`

	// InvoiceFrequency determines how often invoices are generated.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Monthly;Quarterly;Annual
	// +kubebuilder:default=Monthly
	InvoiceFrequency string `json:"invoiceFrequency,omitempty"`

	// InvoiceDayOfMonth is the day of the month invoices are generated.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=28
	// +kubebuilder:default=1
	InvoiceDayOfMonth int32 `json:"invoiceDayOfMonth,omitempty"`
}

// BillingContactInfo defines contact details for billing notifications.
type BillingContactInfo struct {
	// Email is the email address for billing notifications.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Email string `json:"email"`

	// Name is the display name of the billing contact.
	//
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`
}

// BillingAccountStatus defines the observed state of a BillingAccount.
type BillingAccountStatus struct {
	// Phase represents the current lifecycle phase of the billing account.
	//
	// +kubebuilder:validation:Optional
	Phase BillingAccountPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the billing
	// account's state.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LinkedProjectsCount is the number of projects currently bound to this
	// billing account.
	//
	// +kubebuilder:validation:Optional
	LinkedProjectsCount int32 `json:"linkedProjectsCount,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// BillingAccount is the Schema for the billingaccounts API. It represents a
// billing entity within an organization that is responsible for paying for
// service consumption.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=`.spec.currencyCode`
// +kubebuilder:printcolumn:name="Projects",type=integer,JSONPath=`.status.linkedProjectsCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type BillingAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingAccountSpec   `json:"spec,omitempty"`
	Status BillingAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingAccountList contains a list of BillingAccount.
type BillingAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingAccount{}, &BillingAccountList{})
}
