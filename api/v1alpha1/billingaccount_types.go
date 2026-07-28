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

// BillingAccountConditionDefaultPaymentMethodReady is set by the billing
// service controller and reflects whether the account has a usable default
// payment instrument. Downstream services (invoicing, charge processing)
// gate on this condition rather than on account phase.
const BillingAccountConditionDefaultPaymentMethodReady = "DefaultPaymentMethodReady"

// BillingAccountConditionInvoicingReady is set by the billing service
// controller and reflects whether the account's invoices are in a
// healthy payment state. Downstream consumers gate on this condition
// rather than on account phase.
//
// Reasons:
//   - NoInvoicesYet — no Invoice resources exist (True)
//   - Current — readiness is driven by Open/Paid, or only Void invoices remain (True)
//   - PastDue — the newest Open/Paid/PastDue invoice is PastDue (False)
//   - PhasePending — invoices exist but none have a projected phase yet (Unknown)
//
// Void invoices are skipped when evaluating readiness so a newer Void
// cannot mask an older PastDue. status.latestInvoiceRef still points at
// the most recently created Invoice regardless of phase.
const BillingAccountConditionInvoicingReady = "InvoicingReady"

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
	// +kubebuilder:default={}
	PaymentTerms *PaymentTerms `json:"paymentTerms,omitempty"`

	// ContactInfo describes the billing contact and the postal
	// address invoices are issued to.
	//
	// +kubebuilder:validation:Optional
	ContactInfo *BillingContactInfo `json:"contactInfo,omitempty"`

	// TaxIDs are the tax registrations attached to this account. An
	// account can carry multiple entries (e.g. an organisation
	// registered for both GB VAT and EU VAT).
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	TaxIDs []TaxID `json:"taxIds,omitempty"`

	// DefaultPaymentMethodRef references the PaymentMethod to use by
	// default for charge processing. The referenced PaymentMethod must
	// reside in the same namespace and be in the Active phase.
	//
	// +kubebuilder:validation:Optional
	DefaultPaymentMethodRef *DefaultPaymentMethodRef `json:"defaultPaymentMethodRef,omitempty"`
}

// BillingAddress is a postal billing address. Country is required;
// other fields are recommended but optional because postal-address
// conventions differ widely by region.
//
// Name fields are intentionally absent: the recipient name (and
// optional business name) live on BillingContactInfo so we have a
// single source of truth for "who is being billed" and a clean 1:1
// mapping onto provider Customer records (Stripe, Adyen, etc. all
// use a single name field on the customer, not on the address).
type BillingAddress struct {
	// Country is the ISO 3166-1 alpha-2 country code (e.g. "GB",
	// "US"). Required because tax determination and currency
	// restrictions depend on it.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Z]{2}$`
	Country string `json:"country"`

	// Line1 is the first line of the street address (typically
	// "number + street").
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Line1 string `json:"line1,omitempty"`

	// Line2 is the second line of the street address (typically
	// apartment / suite / building).
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Line2 string `json:"line2,omitempty"`

	// City is the locality.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	City string `json:"city,omitempty"`

	// Region is the state, province, or county (free-form).
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	Region string `json:"region,omitempty"`

	// PostalCode is the post / zip code.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=32
	PostalCode string `json:"postalCode,omitempty"`
}

// TaxID is a single tax registration.
//
// Type values follow a vendor-neutral snake-case
// `<jurisdiction>_<scheme>` convention — e.g. "gb_vat", "eu_vat",
// "us_ein", "au_abn", "ca_gst_hst", "ch_vat", "in_gst", "sg_gst".
// The pattern check enforces the shape rather than the exact set of
// values, so new schemes can be added without API changes.
// Translation to any provider-specific identifier (if needed) is the
// responsibility of the provider controller, not this schema.
type TaxID struct {
	// Type identifies the tax registration scheme (e.g. "gb_vat").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z]{2}_[a-z][a-z_]*$`
	// +kubebuilder:validation:MaxLength=32
	Type string `json:"type"`

	// Value is the registration number / identifier
	// (e.g. "GB123456789").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Value string `json:"value"`
}

// DefaultPaymentMethodRef references a PaymentMethod in the same namespace
// as the BillingAccount.
type DefaultPaymentMethodRef struct {
	// Name is the name of the PaymentMethod.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// LatestInvoiceRef references an Invoice in the same namespace as the
// BillingAccount. Set by the billing service controller to the most
// recently created Invoice for the account.
type LatestInvoiceRef struct {
	// Name is the name of the Invoice.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
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

// BillingContactInfo defines contact details for billing notifications,
// the postal address invoices are issued to, and an optional dedicated
// invoice-recipient email.
type BillingContactInfo struct {
	// Email is the primary billing contact email. Receives billing
	// notifications and, when InvoiceEmail is unset, also receives
	// invoices and receipts.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Email string `json:"email"`

	// Name is the display name of the individual billing contact —
	// the human the platform talks to. Surfaces as the "ATTN:" line
	// on invoices when BusinessName is also set.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name,omitempty"`

	// BusinessName is the legal entity that pays. Optional; populate
	// it for B2B accounts so invoices print the company name as the
	// top header line and the provider Customer record carries the
	// company name rather than the individual contact.
	//
	// When set, the provider controller maps this onto its
	// Customer.name field (e.g. Stripe Customer.name). When unset,
	// Name is used instead.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	BusinessName string `json:"businessName,omitempty"`

	// InvoiceEmails is the list of recipients that invoices and
	// receipts are sent to. The first entry is the primary recipient;
	// subsequent entries are CC'd. When the list is empty, Email is
	// used as a single primary recipient.
	//
	// Provider support for multiple recipients varies: Stripe
	// Customer.email is single-valued, so the stripe-provider maps
	// the first entry onto it and CC's the rest via its own
	// invoice-sent webhook (when configured). Consumers should treat
	// the entire list as authoritative; the provider handles fan-out.
	//
	// Duplicate entries are rejected. Maximum 10 entries.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	// +listType=set
	InvoiceEmails []string `json:"invoiceEmails,omitempty"`

	// Address is the postal billing address. Appears on invoices and
	// is surfaced to the configured provider controller (e.g. for
	// tax determination, AVS).
	//
	// +kubebuilder:validation:Optional
	Address *BillingAddress `json:"address,omitempty"`
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

	// LatestInvoiceRef references the most recently created Invoice for
	// this billing account. Cleared when no invoices exist.
	//
	// +kubebuilder:validation:Optional
	LatestInvoiceRef *LatestInvoiceRef `json:"latestInvoiceRef,omitempty"`

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
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.metadata.annotations.kubernetes\.io/display-name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=`.spec.currencyCode`
// +kubebuilder:printcolumn:name="Projects",type=integer,JSONPath=`.status.linkedProjectsCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
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
