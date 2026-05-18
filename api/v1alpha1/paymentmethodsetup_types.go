// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PaymentMethodSetupPhase represents the lifecycle state of a PaymentMethodSetup.
// +kubebuilder:validation:Enum=Pending;ClientSecretReady;Succeeded;Failed
type PaymentMethodSetupPhase string

const (
	// PaymentMethodSetupPhasePending indicates the SetupIntent has not yet
	// been created upstream.
	PaymentMethodSetupPhasePending PaymentMethodSetupPhase = "Pending"

	// PaymentMethodSetupPhaseClientSecretReady indicates the SetupIntent has
	// been created and a clientSecret is available for the browser to
	// confirm with Stripe Elements.
	PaymentMethodSetupPhaseClientSecretReady PaymentMethodSetupPhase = "ClientSecretReady"

	// PaymentMethodSetupPhaseSucceeded indicates the SetupIntent has been
	// confirmed (via webhook) and the resulting PaymentMethod has been
	// attached to the BillingAccount.
	PaymentMethodSetupPhaseSucceeded PaymentMethodSetupPhase = "Succeeded"

	// PaymentMethodSetupPhaseFailed indicates the SetupIntent failed.
	PaymentMethodSetupPhaseFailed PaymentMethodSetupPhase = "Failed"
)

// PaymentMethodSetup condition types.
const (
	// PaymentMethodSetupConditionReady is True when the SetupIntent has been
	// created upstream and a clientSecret is available for the browser.
	PaymentMethodSetupConditionReady = "Ready"

	// PaymentMethodSetupConditionSucceeded is True when the corresponding
	// upstream webhook (e.g. setup_intent.succeeded) has fired and a
	// PaymentMethod has been attached to the BillingAccount.
	PaymentMethodSetupConditionSucceeded = "Succeeded"
)

// PaymentMethodSetupSpec defines the desired state of a PaymentMethodSetup.
//
// Spec is effectively immutable once the controller has created an upstream
// SetupIntent: re-binding fields would orphan the upstream resource. To
// retry a failed setup, create a new PaymentMethodSetup.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type PaymentMethodSetupSpec struct {
	// BillingAccountRef references the BillingAccount this setup belongs
	// to. The BillingAccount is the owner of the resulting PaymentMethod.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// ReturnURL is an optional URL that Stripe will redirect the browser
	// to after 3DS authentication completes. When unset, the front-end
	// is expected to handle the in-page result without a redirect.
	//
	// +kubebuilder:validation:Optional
	ReturnURL string `json:"returnURL,omitempty"`
}

// PaymentMethodSetupStatus defines the observed state of a PaymentMethodSetup.
type PaymentMethodSetupStatus struct {
	// Phase represents the current lifecycle phase of the setup.
	//
	// +kubebuilder:validation:Optional
	Phase PaymentMethodSetupPhase `json:"phase,omitempty"`

	// ClientSecret is the Stripe SetupIntent client_secret value. The
	// browser uses this with Stripe Elements to confirm the setup. The
	// value is single-use and short-lived.
	//
	// +kubebuilder:validation:Optional
	ClientSecret string `json:"clientSecret,omitempty"`

	// PublishableKey is the public Stripe publishable key for the
	// provider that issued this setup. Surfaced here so the browser can
	// bootstrap Stripe Elements from a single resource read.
	//
	// +kubebuilder:validation:Optional
	PublishableKey string `json:"publishableKey,omitempty"`

	// ProviderName is the name of the PaymentProvider that fulfilled
	// this setup.
	//
	// +kubebuilder:validation:Optional
	ProviderName string `json:"providerName,omitempty"`

	// SetupIntentID is the upstream identifier of the SetupIntent
	// (e.g. `seti_…` for Stripe).
	//
	// +kubebuilder:validation:Optional
	SetupIntentID string `json:"setupIntentId,omitempty"`

	// SetupIntentStatus is the most recent upstream status of the
	// SetupIntent (e.g. `requires_payment_method`, `succeeded`).
	//
	// +kubebuilder:validation:Optional
	SetupIntentStatus string `json:"setupIntentStatus,omitempty"`

	// FailureReason is a short, machine-parseable code for the failure
	// (e.g. `card_declined`). Set when phase is Failed.
	//
	// +kubebuilder:validation:Optional
	FailureReason string `json:"failureReason,omitempty"`

	// FailureMessage is a human-readable description of the failure.
	//
	// +kubebuilder:validation:Optional
	FailureMessage string `json:"failureMessage,omitempty"`

	// Conditions represent the latest available observations of the
	// setup's state.
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

// PaymentMethodSetup is the Schema for the paymentmethodsetups API. It
// represents an in-flight payment-method onboarding for a BillingAccount.
// Clients create one of these, then poll `.status.clientSecret` to drive
// Stripe Elements in the browser.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="SetupIntent",type=string,JSONPath=`.status.setupIntentId`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type PaymentMethodSetup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PaymentMethodSetupSpec   `json:"spec,omitempty"`
	Status PaymentMethodSetupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PaymentMethodSetupList contains a list of PaymentMethodSetup.
type PaymentMethodSetupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PaymentMethodSetup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PaymentMethodSetup{}, &PaymentMethodSetupList{})
}
