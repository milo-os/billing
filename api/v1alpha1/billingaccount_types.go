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

	// PaymentProviderRef references the PaymentProvider used to collect
	// and charge against this account's payment method. When unset, the
	// account cannot accept a payment method or transition to Ready.
	//
	// +kubebuilder:validation:Optional
	PaymentProviderRef *PaymentProviderRef `json:"paymentProviderRef,omitempty"`
}

// PaymentProviderRef is a reference to a cluster-scoped PaymentProvider.
type PaymentProviderRef struct {
	// Name is the name of the PaymentProvider.
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

// BillingAccount condition types.
const (
	// BillingAccountConditionPaymentMethodAttached is True when the account
	// has a confirmed payment method available for charging.
	BillingAccountConditionPaymentMethodAttached = "PaymentMethodAttached"

	// BillingAccountConditionPlatformAccessApproved is True when the fraud
	// system has approved the owning user for platform access. This is a
	// mirror of the upstream PlatformAccessApproval and is one of the gates
	// for transitioning the account to Ready.
	BillingAccountConditionPlatformAccessApproved = "PlatformAccessApproved"
)

// PaymentMethodInfo is the sanitized, public-safe metadata about the
// payment method currently attached to a BillingAccount. Raw card data is
// never stored here; everything in this struct is acceptable to expose to
// the BillingAccount owner.
type PaymentMethodInfo struct {
	// ProviderCustomerID is the upstream customer identifier (e.g.
	// Stripe `cus_…`).
	//
	// +kubebuilder:validation:Optional
	ProviderCustomerID string `json:"providerCustomerId,omitempty"`

	// PaymentMethodID is the upstream payment-method identifier (e.g.
	// Stripe `pm_…`).
	//
	// +kubebuilder:validation:Optional
	PaymentMethodID string `json:"paymentMethodId,omitempty"`

	// SetupIntentID is the upstream SetupIntent that produced this
	// payment method.
	//
	// +kubebuilder:validation:Optional
	SetupIntentID string `json:"setupIntentId,omitempty"`

	// Brand is the card network (e.g. `visa`, `mastercard`).
	//
	// +kubebuilder:validation:Optional
	Brand string `json:"brand,omitempty"`

	// Last4 is the last four digits of the card.
	//
	// +kubebuilder:validation:Optional
	Last4 string `json:"last4,omitempty"`

	// BIN is the card issuer identification number (first 6-8 digits).
	// Stored to feed downstream fraud scoring; never the full PAN.
	//
	// +kubebuilder:validation:Optional
	BIN string `json:"bin,omitempty"`

	// Country is the ISO 3166-1 alpha-2 country code of the issuer.
	//
	// +kubebuilder:validation:Optional
	Country string `json:"country,omitempty"`

	// ExpMonth is the card expiration month (1-12).
	//
	// +kubebuilder:validation:Optional
	ExpMonth int32 `json:"expMonth,omitempty"`

	// ExpYear is the card expiration year (four digits).
	//
	// +kubebuilder:validation:Optional
	ExpYear int32 `json:"expYear,omitempty"`

	// AVSResult is the Address Verification System result code returned
	// by the issuer (e.g. `Y`, `N`, `unchecked`).
	//
	// +kubebuilder:validation:Optional
	AVSResult string `json:"avsResult,omitempty"`

	// CVCResult is the CVC verification result returned by the issuer
	// (e.g. `pass`, `fail`, `unchecked`).
	//
	// +kubebuilder:validation:Optional
	CVCResult string `json:"cvcResult,omitempty"`

	// AttachedAt is the time the payment method was attached.
	//
	// +kubebuilder:validation:Optional
	AttachedAt *metav1.Time `json:"attachedAt,omitempty"`
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

	// PaymentMethod is the sanitized metadata for the payment method
	// currently attached to this account. When unset, the account has no
	// usable payment method and cannot be charged.
	//
	// +kubebuilder:validation:Optional
	PaymentMethod *PaymentMethodInfo `json:"paymentMethod,omitempty"`

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
