// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InvoicePhase represents the payment lifecycle state of an Invoice.
// +kubebuilder:validation:Enum=Open;Paid;PastDue;Void
type InvoicePhase string

const (
	// InvoicePhaseOpen indicates the invoice has been issued and payment
	// is outstanding but not yet past due.
	InvoicePhaseOpen InvoicePhase = "Open"

	// InvoicePhasePaid indicates the invoice has been paid in full
	// (amountDue has reached zero).
	InvoicePhasePaid InvoicePhase = "Paid"

	// InvoicePhasePastDue indicates the invoice remains unpaid past its
	// due date.
	InvoicePhasePastDue InvoicePhase = "PastDue"

	// InvoicePhaseVoid indicates the invoice has been voided and is no
	// longer collectible.
	InvoicePhaseVoid InvoicePhase = "Void"
)

// InvoiceConditionReady is set on Invoice by the owning invoicing
// provider once status has been fully projected. Reason values include
// Paid and CurrencyMismatch.
const InvoiceConditionReady = "Ready"

// InvoiceSpec defines the desired state of an Invoice.
//
// Invoice is created and updated exclusively by an invoicing provider —
// never by a consumer or the portal. Spec fields are immutable once set.
type InvoiceSpec struct {
	// BillingAccountRef references the BillingAccount this invoice
	// belongs to. The BillingAccount must reside in the same namespace.
	// Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="billingAccountRef is immutable"
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// Period is the billing period this invoice covers. Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="period is immutable"
	Period InvoicePeriod `json:"period"`
}

// InvoicePeriod is the closed billing window covered by an Invoice.
type InvoicePeriod struct {
	// Start is the inclusive start of the billing period.
	//
	// +kubebuilder:validation:Required
	Start metav1.Time `json:"start"`

	// End is the inclusive end of the billing period.
	//
	// +kubebuilder:validation:Required
	End metav1.Time `json:"end"`
}

// InvoiceStatus defines the observed state of an Invoice. Populated
// exclusively by the invoicing provider.
type InvoiceStatus struct {
	// Phase represents the current payment lifecycle phase.
	//
	// +kubebuilder:validation:Optional
	Phase InvoicePhase `json:"phase,omitempty"`

	// CurrencyCode is the ISO 4217 currency code for the invoice totals.
	// Must match BillingAccount.spec.currencyCode; providers that detect
	// a mismatch surface Ready=False with reason CurrencyMismatch.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[A-Z]{3}$`
	CurrencyCode string `json:"currencyCode,omitempty"`

	// Total is the invoice total as computed by the provider, expressed
	// as a decimal string (e.g. "482.19").
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^-?[0-9]+(\.[0-9]+)?$`
	Total string `json:"total,omitempty"`

	// AmountPaid is the amount collected so far, as a decimal string.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^-?[0-9]+(\.[0-9]+)?$`
	AmountPaid string `json:"amountPaid,omitempty"`

	// AmountDue is the remaining balance. Provider-authoritative — not
	// derived client-side from total and amountPaid.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^-?[0-9]+(\.[0-9]+)?$`
	AmountDue string `json:"amountDue,omitempty"`

	// DueDate is when payment is due.
	//
	// +kubebuilder:validation:Optional
	DueDate *metav1.Time `json:"dueDate,omitempty"`

	// PaidAt is set once amountDue reaches zero.
	//
	// +kubebuilder:validation:Optional
	PaidAt *metav1.Time `json:"paidAt,omitempty"`

	// DocumentUri is a provider-hosted link to the human-readable
	// invoice document (PDF or HTML).
	//
	// +kubebuilder:validation:Optional
	DocumentURI string `json:"documentUri,omitempty"`

	// Conditions represent the latest available observations of the
	// invoice's state. See InvoiceConditionReady.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// reconciling provider controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// Invoice is the Schema for the invoices API.
//
// Invoice is the provider-written, vendor-agnostic record of a billing
// account's invoice for a period. Names are deterministic
// (`<billing-account>-<year>-<month>`) so creation is idempotent.
// Vendor identifiers live as provider-prefixed annotations, not typed
// fields.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Total",type=string,JSONPath=`.status.total`
// +kubebuilder:printcolumn:name="Amount Due",type=string,JSONPath=`.status.amountDue`
// +kubebuilder:printcolumn:name="Due",type=string,JSONPath=`.status.dueDate`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type Invoice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InvoiceSpec   `json:"spec,omitempty"`
	Status InvoiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InvoiceList contains a list of Invoice.
type InvoiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Invoice `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Invoice{}, &InvoiceList{})
}
