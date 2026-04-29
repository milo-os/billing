// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MonitoredResourceTypeSpec defines the desired state of a
// MonitoredResourceType.
//
// A MonitoredResourceType declares which Kubernetes Kinds are billable
// or otherwise monitored by the platform, and the closed set of
// descriptive labels that usage events against that Kind may carry.
//
// Core fields (resourceTypeName, gvk.group, gvk.kind) are immutable once
// created; a breaking change ships as a new MonitoredResourceType with a
// new canonical name.
//
// spec.phase is the provider-declared lifecycle intent: Draft ->
// Published -> Deprecated -> Retired. The controller mirrors that
// intent via conditions; it does not transition the phase itself.
//
// Ownership is expressed via labels rather than a spec.owner field.
type MonitoredResourceTypeSpec struct {
	// ResourceTypeName is the canonical, user-facing identifier for
	// this monitored resource type. It typically combines the owning
	// service's reverse-DNS name with the Kubernetes Kind (e.g.
	// "compute.miloapis.com/Instance") and is the stable identifier
	// used by portal drill-downs, FinOps exports, and billing events.
	// Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="resourceTypeName is immutable"
	ResourceTypeName string `json:"resourceTypeName"`

	// Phase is the provider-declared lifecycle state of this
	// monitored resource type. Allowed transitions are forward-only:
	// Draft -> Published -> Deprecated -> Retired.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Draft;Published;Deprecated;Retired
	// +kubebuilder:default=Draft
	Phase Phase `json:"phase"`

	// DisplayName is a human-readable name surfaced in portals and
	// dashboards alongside the canonical resourceTypeName. Editable
	// over the type's lifetime.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// Description is a plain-English explanation of what the resource
	// type represents. Editable over the type's lifetime.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// GVK pins the resource type to a Kubernetes Kind. Version is
	// deliberately omitted: billability is a property of the Kind, not
	// of a specific API version.
	//
	// +kubebuilder:validation:Required
	GVK MonitoredResourceTypeGVK `json:"gvk"`

	// Labels is the closed set of descriptive labels that usage events
	// against this resource type are permitted to carry. Events whose
	// labels are not in this set are rejected at the edge. Adding a new
	// optional label is additive; adding a required label or removing
	// any declared label is a breaking change.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Labels []MonitoredResourceLabel `json:"labels,omitempty"`
}

// MonitoredResourceTypeGVK identifies the Kubernetes Kind this resource
// type is bound to. Version is intentionally excluded so that API
// version evolution does not require a new MonitoredResourceType.
type MonitoredResourceTypeGVK struct {
	// Group is the Kubernetes API group of the Kind (e.g.
	// "compute.miloapis.com"). Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="gvk.group is immutable"
	Group string `json:"group"`

	// Kind is the Kubernetes Kind (e.g. "Instance"). Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="gvk.kind is immutable"
	Kind string `json:"kind"`
}

// MonitoredResourceLabel declares a single descriptive label that usage
// events against the resource type may carry. Labels form a closed set:
// events bearing any label not declared here are rejected before they
// reach the audit log.
type MonitoredResourceLabel struct {
	// Name is the label key as it will appear on usage events (e.g.
	// "region", "zone", "tier"). It is the map key for this list.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Required indicates whether every usage event against this
	// resource type must carry this label. Defaults to false
	// (optional).
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	Required bool `json:"required,omitempty"`

	// Description is a plain-English explanation of what the label
	// conveys. Editable over the resource type's lifetime.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=512
	Description string `json:"description,omitempty"`

	// AllowedValues, when non-empty, constrains the label to a fixed
	// vocabulary. Events carrying a value outside this set are rejected.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=atomic
	AllowedValues []string `json:"allowedValues,omitempty"`
}

// MonitoredResourceTypeStatus defines the observed state of a
// MonitoredResourceType.
type MonitoredResourceTypeStatus struct {
	// CatalogStatus embeds the shared catalog lifecycle fields
	// (publishedAt, conditions, observedGeneration). Phase lives on
	// spec; status mirrors it via the Published condition.
	CatalogStatus `json:",inline"`
}

// MonitoredResourceType is the Schema for the monitoredresourcetypes
// API. It is the platform's declaration of which Kubernetes Kinds can
// appear on a bill or a dashboard, and what descriptive labels their
// usage events are allowed to carry.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.resourceTypeName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.spec.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type MonitoredResourceType struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MonitoredResourceTypeSpec   `json:"spec,omitempty"`
	Status MonitoredResourceTypeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MonitoredResourceTypeList contains a list of MonitoredResourceType.
type MonitoredResourceTypeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MonitoredResourceType `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MonitoredResourceType{}, &MonitoredResourceTypeList{})
}
