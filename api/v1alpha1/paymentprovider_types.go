// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PaymentProviderType identifies the upstream payment processor.
// +kubebuilder:validation:Enum=stripe
type PaymentProviderType string

const (
	// PaymentProviderTypeStripe is the Stripe payment processor.
	PaymentProviderTypeStripe PaymentProviderType = "stripe"
)

// PaymentProviderMode toggles between test and live processor environments.
// +kubebuilder:validation:Enum=test;live
type PaymentProviderMode string

const (
	PaymentProviderModeTest PaymentProviderMode = "test"
	PaymentProviderModeLive PaymentProviderMode = "live"
)

// PaymentProviderPhase represents the lifecycle state of a PaymentProvider.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type PaymentProviderPhase string

const (
	PaymentProviderPhasePending PaymentProviderPhase = "Pending"
	PaymentProviderPhaseReady   PaymentProviderPhase = "Ready"
	PaymentProviderPhaseFailed  PaymentProviderPhase = "Failed"
)

// PaymentProviderSpec defines the desired state of a PaymentProvider.
type PaymentProviderSpec struct {
	// Type identifies which upstream processor this provider talks to.
	//
	// +kubebuilder:validation:Required
	Type PaymentProviderType `json:"type"`

	// Mode selects the processor environment (test vs live keys).
	//
	// +kubebuilder:validation:Required
	Mode PaymentProviderMode `json:"mode"`

	// Config holds processor-specific configuration. Exactly one
	// sub-config should be set, matching `Type`.
	//
	// +kubebuilder:validation:Required
	Config PaymentProviderConfig `json:"config"`
}

// PaymentProviderConfig is a union of provider-specific configurations.
type PaymentProviderConfig struct {
	// Stripe is the configuration for the Stripe provider. Required
	// when `spec.type` is `stripe`.
	//
	// +kubebuilder:validation:Optional
	Stripe *StripeProviderConfig `json:"stripe,omitempty"`
}

// StripeProviderConfig configures the Stripe payment provider.
type StripeProviderConfig struct {
	// PublishableKeyRef is a reference to a Secret key containing the
	// Stripe publishable key. The value is non-sensitive and is surfaced
	// to clients via PaymentMethodSetup.status.publishableKey.
	//
	// +kubebuilder:validation:Required
	PublishableKeyRef corev1.SecretKeySelector `json:"publishableKeyRef"`

	// SecretKeyRef is a reference to a Secret key containing the Stripe
	// secret API key used for server-to-server calls.
	//
	// +kubebuilder:validation:Required
	SecretKeyRef corev1.SecretKeySelector `json:"secretKeyRef"`

	// WebhookSecretRef is a reference to a Secret key containing the
	// signing secret used to verify Stripe webhook payloads.
	//
	// +kubebuilder:validation:Required
	WebhookSecretRef corev1.SecretKeySelector `json:"webhookSecretRef"`

	// APIVersion pins the Stripe API version used for outbound requests.
	// When unset, the SDK default is used.
	//
	// +kubebuilder:validation:Optional
	APIVersion string `json:"apiVersion,omitempty"`
}

// PaymentProviderStatus defines the observed state of a PaymentProvider.
type PaymentProviderStatus struct {
	// Phase represents the current lifecycle phase of the provider.
	//
	// +kubebuilder:validation:Optional
	Phase PaymentProviderPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the
	// provider's state.
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

// PaymentProvider is the Schema for the paymentproviders API. It defines
// a configured upstream payment processor that BillingAccounts can use to
// collect payment methods and (eventually) charge invoices.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type PaymentProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PaymentProviderSpec   `json:"spec,omitempty"`
	Status PaymentProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PaymentProviderList contains a list of PaymentProvider.
type PaymentProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PaymentProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PaymentProvider{}, &PaymentProviderList{})
}
