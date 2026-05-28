// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsDefaultPaymentMethodClassLabel marks a PaymentMethodClass as the
// cluster default. Exactly one class may carry this label set to
// "true"; the defaulting webhook for PaymentMethod injects the labelled
// class onto any PaymentMethod that does not specify one.
//
// Implemented as a label (rather than an annotation) so consumers can
// select the default class via a label selector — e.g.
// client.MatchingLabels{IsDefaultPaymentMethodClassLabel: "true"} —
// instead of listing every class and filtering in user space.
const IsDefaultPaymentMethodClassLabel = "billing.miloapis.com/is-default-class"

// PaymentMethodClassSpec defines the desired state of a PaymentMethodClass.
type PaymentMethodClassSpec struct {
	// Provider is the name of the provider controller responsible for
	// reconciling PaymentMethods of this class. The controller watches
	// PaymentMethods whose spec.paymentMethodClassRef points at a class
	// whose spec.provider matches its own identity. Examples: "stripe",
	// "braintree".
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`

	// ParametersRef points at a provider-owned resource carrying any
	// provider-specific SDK configuration (e.g. a Stripe publishable
	// key). PaymentMethodClass intentionally carries no provider-specific
	// fields — adding a new provider must not require a billing-service
	// schema change. This mirrors the Kubernetes Gateway API
	// `parametersRef` pattern.
	//
	// +kubebuilder:validation:Required
	ParametersRef PaymentMethodClassParametersRef `json:"parametersRef"`
}

// PaymentMethodClassParametersRef points at a provider-specific
// configuration resource.
type PaymentMethodClassParametersRef struct {
	// Group is the API group of the provider-specific configuration
	// resource (e.g. "stripe.billing.miloapis.com").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// Kind is the Kind of the provider-specific configuration resource
	// (e.g. "StripeProviderConfig").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name is the name of the provider-specific configuration resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PaymentMethodClassStatus defines the observed state of a PaymentMethodClass.
type PaymentMethodClassStatus struct {
	// Conditions represent the latest available observations of the
	// class's state.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// PublishableKey is the non-sensitive client-side identifier resolved
	// from spec.parametersRef. The PaymentMethodClass reconciler reads
	// .spec.publishableKey from the referenced provider configuration
	// resource (e.g. StripeProviderConfig.spec.publishableKey) and
	// projects it onto status so consumers can read it without needing
	// IAM access to the provider's own API group. Empty until the
	// reconciler has successfully resolved the reference.
	//
	// +kubebuilder:validation:Optional
	PublishableKey string `json:"publishableKey,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// Condition types surfaced on PaymentMethodClass.status.conditions.
const (
	// PaymentMethodClassParametersResolved indicates whether the
	// reconciler has successfully fetched spec.parametersRef and
	// projected its non-sensitive fields (publishableKey) onto status.
	PaymentMethodClassParametersResolved = "ParametersResolved"
)

// PaymentMethodClass is the Schema for the paymentmethodclasses API.
//
// PaymentMethodClass is a cluster-scoped resource configured by platform
// operators. It names the payment provider controller responsible for
// reconciling PaymentMethods of this class, and references provider-owned
// configuration via spec.parametersRef. Consumers do not interact with
// PaymentMethodClass directly — the billing service defaulting webhook
// injects the cluster default class onto PaymentMethods at creation time.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Default",type=string,JSONPath=`.metadata.labels.billing\.miloapis\.com/is-default-class`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type PaymentMethodClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PaymentMethodClassSpec   `json:"spec,omitempty"`
	Status PaymentMethodClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PaymentMethodClassList contains a list of PaymentMethodClass.
type PaymentMethodClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PaymentMethodClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PaymentMethodClass{}, &PaymentMethodClassList{})
}
