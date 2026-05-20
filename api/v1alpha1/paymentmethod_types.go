// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PaymentMethodPhase represents the lifecycle state of a PaymentMethod.
// +kubebuilder:validation:Enum=Pending;AwaitingConfirmation;Active;Failed
type PaymentMethodPhase string

const (
	// PaymentMethodPhasePending indicates the resource has been created
	// but the provider controller has not yet taken ownership.
	PaymentMethodPhasePending PaymentMethodPhase = "Pending"

	// PaymentMethodPhaseAwaitingConfirmation indicates the provider has
	// started a setup session and is waiting for the consumer to confirm
	// the instrument (e.g. Stripe SetupIntent created, clientSecret
	// available to the browser).
	PaymentMethodPhaseAwaitingConfirmation PaymentMethodPhase = "AwaitingConfirmation"

	// PaymentMethodPhaseActive indicates the instrument has been confirmed
	// and is usable for charges.
	PaymentMethodPhaseActive PaymentMethodPhase = "Active"

	// PaymentMethodPhaseFailed indicates the setup flow ended without a
	// usable instrument (declined card, expired SetupIntent, etc.).
	PaymentMethodPhaseFailed PaymentMethodPhase = "Failed"
)

// PaymentMethodConditionInstrumentReady is set on PaymentMethod by the
// owning provider controller once a usable instrument is attached. This is
// the authoritative signal for downstream services that the payment
// method can be charged.
const PaymentMethodConditionInstrumentReady = "InstrumentReady"

// PaymentMethodInstrumentType identifies the broad category of payment
// instrument. Additional types are added as providers are integrated.
// +kubebuilder:validation:Enum=card;usBankAccount
type PaymentMethodInstrumentType string

const (
	PaymentMethodInstrumentTypeCard          PaymentMethodInstrumentType = "card"
	PaymentMethodInstrumentTypeUSBankAccount PaymentMethodInstrumentType = "usBankAccount"
)

// PaymentMethodSpec defines the desired state of a PaymentMethod.
type PaymentMethodSpec struct {
	// BillingAccountRef references the BillingAccount this payment method
	// belongs to. The BillingAccount must reside in the same namespace.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// DisplayName is a human-readable label shown in the portal and on
	// invoices (e.g. "Corporate Visa").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// PaymentMethodClassRef selects the PaymentMethodClass — and through
	// it the provider controller — that owns the setup flow for this
	// payment method. Left unset by consumers and injected by the
	// defaulting webhook from the cluster default class. Immutable once
	// set; changing it would orphan provider-side state.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="paymentMethodClassRef is immutable once set"
	PaymentMethodClassRef *PaymentMethodClassRef `json:"paymentMethodClassRef,omitempty"`
}

// PaymentMethodClassRef references a cluster-scoped PaymentMethodClass.
type PaymentMethodClassRef struct {
	// Name is the name of the PaymentMethodClass.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PaymentMethodDetails is the normalized, provider-agnostic description of
// a confirmed payment instrument. Provider-specific identifiers (Stripe
// payment method ids, customer ids, etc.) are not exposed here — they
// live on the provider-owned CRD and are consumed only by the provider
// controller.
type PaymentMethodDetails struct {
	// Type identifies the instrument category.
	//
	// +kubebuilder:validation:Required
	Type PaymentMethodInstrumentType `json:"type"`

	// Card is populated when Type is `card`.
	//
	// +kubebuilder:validation:Optional
	Card *PaymentMethodCardDetails `json:"card,omitempty"`

	// USBankAccount is populated when Type is `usBankAccount`.
	//
	// +kubebuilder:validation:Optional
	USBankAccount *PaymentMethodUSBankAccountDetails `json:"usBankAccount,omitempty"`
}

// PaymentMethodCardDetails carries the public-safe metadata of a confirmed
// card instrument. Every issuer-returned field that is meaningful across
// processors belongs here; provider-specific identifiers do not. Raw card
// data (PAN, CVC) is never persisted.
type PaymentMethodCardDetails struct {
	// Brand is the card network (e.g. "visa", "mastercard", "amex").
	//
	// +kubebuilder:validation:Optional
	Brand string `json:"brand,omitempty"`

	// Last4 is the last four digits of the card number.
	//
	// +kubebuilder:validation:Optional
	Last4 string `json:"last4,omitempty"`

	// IssuerIdentificationNumber is the BIN (first 6-8 digits) of the
	// card. Issuer-returned, not provider-specific; useful for downstream
	// fraud scoring.
	//
	// +kubebuilder:validation:Optional
	IssuerIdentificationNumber string `json:"issuerIdentificationNumber,omitempty"`

	// Country is the ISO 3166-1 alpha-2 country code of the issuer.
	//
	// +kubebuilder:validation:Optional
	Country string `json:"country,omitempty"`

	// ExpiryMonth is the card expiration month (1-12).
	//
	// +kubebuilder:validation:Optional
	ExpiryMonth int32 `json:"expiryMonth,omitempty"`

	// ExpiryYear is the card expiration year (four digits).
	//
	// +kubebuilder:validation:Optional
	ExpiryYear int32 `json:"expiryYear,omitempty"`

	// AVSResult is the Address Verification System result code returned
	// by the issuer (e.g. "pass", "fail", "unchecked").
	//
	// +kubebuilder:validation:Optional
	AVSResult string `json:"avsResult,omitempty"`

	// CVCResult is the card verification value check result returned by
	// the issuer (e.g. "pass", "fail", "unchecked").
	//
	// +kubebuilder:validation:Optional
	CVCResult string `json:"cvcResult,omitempty"`

	// BillingAddress is the cardholder address captured by the
	// provider's collection UI (Stripe's `billing_details.address` on
	// the confirmed PaymentMethod). May differ from the
	// BillingAccount's address — both signals are useful to downstream
	// consumers (the portal for display, fraud for mismatch detection).
	//
	// +kubebuilder:validation:Optional
	BillingAddress *CardBillingAddress `json:"billingAddress,omitempty"`
}

// CardBillingAddress is the cardholder address attached to a confirmed
// payment instrument. Subset of BillingAddress — no name fields because
// the provider returns those separately under billing_details.name.
type CardBillingAddress struct {
	// Country is the ISO 3166-1 alpha-2 country code of the
	// cardholder address.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[A-Z]{2}$`
	Country string `json:"country,omitempty"`

	// Line1 is the first line of the street address.
	//
	// +kubebuilder:validation:Optional
	Line1 string `json:"line1,omitempty"`

	// Line2 is the second line of the street address.
	//
	// +kubebuilder:validation:Optional
	Line2 string `json:"line2,omitempty"`

	// City is the locality.
	//
	// +kubebuilder:validation:Optional
	City string `json:"city,omitempty"`

	// Region is the state, province, or county.
	//
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty"`

	// PostalCode is the post / zip code.
	//
	// +kubebuilder:validation:Optional
	PostalCode string `json:"postalCode,omitempty"`
}

// PaymentMethodUSBankAccountDetails carries the public-safe metadata of a
// confirmed US bank account instrument.
type PaymentMethodUSBankAccountDetails struct {
	// BankName is the name of the bank holding the account.
	//
	// +kubebuilder:validation:Optional
	BankName string `json:"bankName,omitempty"`

	// Last4 is the last four digits of the account number.
	//
	// +kubebuilder:validation:Optional
	Last4 string `json:"last4,omitempty"`

	// AccountType is the type of account (e.g. "checking", "savings").
	//
	// +kubebuilder:validation:Optional
	AccountType string `json:"accountType,omitempty"`
}

// PaymentMethodStatus defines the observed state of a PaymentMethod.
type PaymentMethodStatus struct {
	// Phase represents the current lifecycle phase.
	//
	// +kubebuilder:validation:Optional
	Phase PaymentMethodPhase `json:"phase,omitempty"`

	// Details is the normalized, provider-agnostic description of the
	// confirmed instrument. Populated by the owning provider controller
	// once the phase reaches Active.
	//
	// +kubebuilder:validation:Optional
	Details *PaymentMethodDetails `json:"details,omitempty"`

	// FailureReason is a short, machine-parseable code for the failure
	// (e.g. "card_declined", "setup_intent_expired"). Set when phase is
	// Failed.
	//
	// +kubebuilder:validation:Optional
	FailureReason string `json:"failureReason,omitempty"`

	// FailureMessage is a human-readable description of the failure.
	//
	// +kubebuilder:validation:Optional
	FailureMessage string `json:"failureMessage,omitempty"`

	// Conditions represent the latest available observations of the
	// payment method's state. See PaymentMethodConditionInstrumentReady.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// reconciling controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// PaymentMethod is the Schema for the paymentmethods API.
//
// PaymentMethod associates a payment instrument with a BillingAccount.
// Consumers create it carrying only a BillingAccount reference and a
// display name; the billing service defaulting webhook injects the
// PaymentMethodClass that selects the provider. The provider controller
// (e.g. stripe-provider) drives the setup flow via a provider-owned CRD
// and projects normalized outcome data onto status once the instrument is
// confirmed.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Class",type=string,JSONPath=`.spec.paymentMethodClassRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Brand",type=string,JSONPath=`.status.details.card.brand`
// +kubebuilder:printcolumn:name="Last4",type=string,JSONPath=`.status.details.card.last4`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type PaymentMethod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PaymentMethodSpec   `json:"spec,omitempty"`
	Status PaymentMethodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PaymentMethodList contains a list of PaymentMethod.
type PaymentMethodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PaymentMethod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PaymentMethod{}, &PaymentMethodList{})
}
