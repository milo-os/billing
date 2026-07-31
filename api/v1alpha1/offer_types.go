// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OfferSpec defines the desired state of an Offer.
//
// An Offer bundles ServicePricings into a named, versioned tier. While
// Draft, the platform owner references ServicePricings by name via
// servicePricingRefs. On publish (launchStage=GA) the controller snapshots
// rates into servicePricings; afterward the Offer is immutable except for
// the kubernetes.io/display-name annotation.
type OfferSpec struct {
	// ChargeTypes enumerates every charge type present in the referenced
	// (or snapshotted) pricings. Required so controllers can gate on
	// charge type explicitly.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ChargeTypes []ChargeType `json:"chargeTypes"`

	// LaunchStage is the Offer lifecycle stage. Draft Offers are mutable
	// and not assignable; GA Offers are published and immutable except for
	// the display-name annotation.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Draft;GA
	// +kubebuilder:default=Draft
	LaunchStage OfferLaunchStage `json:"launchStage"`

	// ServicePricingRefs names the ServicePricing resources to include.
	// Authored while Draft; retained for audit after publish. Runtime
	// rating uses servicePricings only.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	ServicePricingRefs []ServicePricingRef `json:"servicePricingRefs,omitempty"`

	// ServicePricings is an immutable snapshot of referenced
	// ServicePricing specs, written by the controller when launchStage
	// first becomes GA. Empty while Draft.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	ServicePricings []ServicePricingSnapshot `json:"servicePricings,omitempty"`
}

// OfferStatus defines the observed state of an Offer.
type OfferStatus struct {
	// PublishedAt is the time at which the controller first observed the
	// Offer in the GA launch stage with a complete snapshot.
	//
	// +kubebuilder:validation:Optional
	PublishedAt *metav1.Time `json:"publishedAt,omitempty"`

	// Conditions represent the latest available observations of the
	// Offer's state.
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

// Offer is the Schema for the offers API. A named, versioned bundle of
// service pricings (a "tier") that billing accounts are entitled to via
// BillingEntitlement.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="LaunchStage",type=string,JSONPath=`.spec.launchStage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
// +genclient
type Offer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OfferSpec   `json:"spec,omitempty"`
	Status OfferStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OfferList contains a list of Offer.
type OfferList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Offer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Offer{}, &OfferList{})
}
