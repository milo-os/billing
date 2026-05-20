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

	// ContactInfo identifies the human responsible for this account.
	// This contact receives security-relevant notifications (auth
	// alerts, ownership transfers, etc.). Invoice delivery is governed
	// separately via BillingDetails.InvoiceEmail.
	//
	// +kubebuilder:validation:Optional
	ContactInfo *BillingContactInfo `json:"contactInfo,omitempty"`

	// BillingDetails carries the postal billing address, tax
	// registrations, and an optional dedicated invoice-recipient email
	// for this account. All three propagate to the payment provider
	// (e.g. Stripe Customer.address / .tax_ids / .email) on every
	// reconcile.
	//
	// +kubebuilder:validation:Optional
	BillingDetails *BillingDetails `json:"billingDetails,omitempty"`

	// DefaultPaymentMethodRef references the PaymentMethod to use by
	// default for charge processing. The referenced PaymentMethod must
	// reside in the same namespace and be in the Active phase. The
	// admission webhook validates both conditions on writes.
	//
	// Holding default on BillingAccount (rather than a flag on each
	// PaymentMethod) avoids the race where two payment methods can both
	// claim the default flag before reconciliation converges.
	//
	// +kubebuilder:validation:Optional
	DefaultPaymentMethodRef *DefaultPaymentMethodRef `json:"defaultPaymentMethodRef,omitempty"`
}

// BillingDetails describes where invoices go, what address appears on
// them, and which tax registrations apply. These are account-level
// facts — changes affect upcoming invoices but do not retroactively
// modify already-issued invoices (provider behaviour mirrors this).
type BillingDetails struct {
	// Address is the postal billing address. Stamped onto the payment
	// provider's Customer record (e.g. Stripe Customer.address) and
	// printed on invoices. Also forwarded to downstream fraud scoring.
	//
	// +kubebuilder:validation:Optional
	Address *BillingAddress `json:"address,omitempty"`

	// TaxIDs are the tax registrations attached to this account. An
	// account can carry multiple entries (e.g. an organisation
	// registered for both GB VAT and EU VAT). Synced to the provider
	// (e.g. Stripe Customer.tax_ids) and rendered on invoices.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	TaxIDs []TaxID `json:"taxIds,omitempty"`

	// InvoiceEmail overrides spec.contactInfo.email as the destination
	// for invoices and receipts. When unset, contactInfo.email is used.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`
	InvoiceEmail string `json:"invoiceEmail,omitempty"`
}

// BillingAddress is a postal billing address. Country is required;
// other fields are recommended but optional because postal-address
// conventions differ widely by region.
type BillingAddress struct {
	// FirstName is the given name of the bill recipient.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	FirstName string `json:"firstName,omitempty"`

	// LastName is the family name of the bill recipient.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	LastName string `json:"lastName,omitempty"`

	// Country is the ISO 3166-1 alpha-2 country code (e.g. "GB",
	// "US"). Required because providers need it to determine tax
	// treatment and currency restrictions.
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

// TaxID is a single tax registration. Type values follow the upstream
// payment-provider vocabulary (Stripe's `tax_id_data.type`) — for
// example "gb_vat", "eu_vat", "us_ein", "au_abn", "ca_gst_hst",
// "ch_vat", "in_gst", "sg_gst". The pattern check enforces the shape
// without locking us out of new types Stripe (and other providers) add
// over time.
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
