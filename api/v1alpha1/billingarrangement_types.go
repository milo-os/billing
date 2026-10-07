// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingArrangementType identifies how an account on an arrangement is
// billed instead of by card. Sponsored access is intentionally absent:
// it will be modelled as a credit grant once invoicing supports credits,
// rather than as a non-billable period.
// +kubebuilder:validation:Enum=Invoice
type BillingArrangementType string

const (
	// BillingArrangementTypeInvoice bills the account by invoice, paid by
	// bank transfer, ACH or wire within the agreed number of net days.
	BillingArrangementTypeInvoice BillingArrangementType = "Invoice"
)

// BillingArrangementPhase represents where an arrangement sits in its
// validity window. Derived from spec.startsAt and spec.endsAt.
// +kubebuilder:validation:Enum=Scheduled;Active;Ended
type BillingArrangementPhase string

const (
	// BillingArrangementPhaseScheduled indicates spec.startsAt is in the
	// future.
	BillingArrangementPhaseScheduled BillingArrangementPhase = "Scheduled"

	// BillingArrangementPhaseActive indicates the arrangement is in effect
	// and counts towards the billing account's PaymentReady condition.
	BillingArrangementPhaseActive BillingArrangementPhase = "Active"

	// BillingArrangementPhaseEnded indicates spec.endsAt has passed.
	BillingArrangementPhaseEnded BillingArrangementPhase = "Ended"
)

// BillingArrangementConditionActive is True while the arrangement is in
// its validity window.
const BillingArrangementConditionActive = "Active"

// BillingArrangementSpec defines the desired state of a BillingArrangement.
//
// +kubebuilder:validation:XValidation:rule="self.type != 'Invoice' || has(self.invoice)",message="invoice is required when type is Invoice"
type BillingArrangementSpec struct {
	// BillingAccountRef references the BillingAccount in the same
	// namespace that these terms apply to. Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="billingAccountRef is immutable"
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// Type selects how the account is billed. Immutable; to change type,
	// end this arrangement and create a new one.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type BillingArrangementType `json:"type"`

	// Invoice carries the invoice terms. Required when type is Invoice.
	//
	// +kubebuilder:validation:Optional
	Invoice *InvoiceArrangementTerms `json:"invoice,omitempty"`

	// StartsAt is when the arrangement takes effect. Defaulted to the
	// time of creation when omitted.
	//
	// +kubebuilder:validation:Optional
	StartsAt *metav1.Time `json:"startsAt,omitempty"`

	// EndsAt is when the arrangement stops applying. Leave unset for terms
	// with no fixed end. Staff end terms early by setting this to the
	// current time, which keeps the record for history.
	//
	// +kubebuilder:validation:Optional
	EndsAt *metav1.Time `json:"endsAt,omitempty"`

	// Reason records why staff granted these terms (for example "Signed
	// MSA, sales call 2026-10-01"). Internal: never shown to customers.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason"`
}

// InvoiceArrangementTerms are the terms an invoice-billed account is held
// to. They're published on BillingAccount status for finance and, later,
// the automated invoicing pipeline.
type InvoiceArrangementTerms struct {
	// NetDays is the number of days after the invoice date that payment
	// is due.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=90
	// +kubebuilder:default=30
	NetDays int32 `json:"netDays,omitempty"`

	// CreditLimit is the maximum spend per billing cycle, as a decimal
	// string in the billing account's currency (e.g. "10000"). Staff are
	// alerted as the account nears it; it is not a hard cap.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)(\.\d+)?$`
	CreditLimit string `json:"creditLimit"`

	// AccountsPayableEmail receives invoices in addition to the billing
	// contact. When unset, invoices go to the billing contact only.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=254
	AccountsPayableEmail string `json:"accountsPayableEmail,omitempty"`

	// PurchaseOrderNumber is printed on invoices.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=64
	PurchaseOrderNumber string `json:"purchaseOrderNumber,omitempty"`

	// AgreementReference identifies a signed MSA or order form, when one
	// exists. Without one, our standard terms of service apply.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	AgreementReference string `json:"agreementReference,omitempty"`
}

// BillingArrangementStatus defines the observed state of a
// BillingArrangement.
type BillingArrangementStatus struct {
	// Phase is where the arrangement sits in its validity window.
	//
	// +kubebuilder:validation:Optional
	Phase BillingArrangementPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the
	// arrangement's state. See BillingArrangementConditionActive.
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

// BillingArrangement is the Schema for the billingarrangements API.
//
// A BillingArrangement records payment terms staff have granted to a
// billing account, letting it be billed without a card. Only staff can
// create or change arrangements; customers see the active terms through
// BillingAccount status.paymentArrangement and the PaymentReady
// condition. Arrangements for the same account may not overlap in time.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Starts",type=date,JSONPath=`.spec.startsAt`
// +kubebuilder:printcolumn:name="Ends",type=date,JSONPath=`.spec.endsAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type BillingArrangement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingArrangementSpec   `json:"spec,omitempty"`
	Status BillingArrangementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingArrangementList contains a list of BillingArrangement.
type BillingArrangementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingArrangement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingArrangement{}, &BillingArrangementList{})
}
