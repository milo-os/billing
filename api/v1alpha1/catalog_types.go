// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase is the lifecycle phase for catalog resources.
// +kubebuilder:validation:Enum=Draft;Published;Deprecated;Retired
type Phase string

const (
	PhaseDraft      Phase = "Draft"
	PhasePublished  Phase = "Published"
	PhaseDeprecated Phase = "Deprecated"
	PhaseRetired    Phase = "Retired"
)

// CatalogStatus is the shared status shape for catalog resources.
type CatalogStatus struct {
	// PublishedAt is the time at which the controller first observed the
	// resource in the Published phase.
	//
	// +kubebuilder:validation:Optional
	PublishedAt *metav1.Time `json:"publishedAt,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
