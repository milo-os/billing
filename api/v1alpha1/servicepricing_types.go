// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServicePricingSpec defines the desired state of a ServicePricing.
//
// A ServicePricing is a price the platform sets, authored as a manifest
// next to the service's meters. A Usage pricing selects one meter by name;
// any number of pricings may select the same meter, and each Offer picks the
// one it bills. OneTime and Recurring pricings name a service and carry a
// fixed amount. Providers read the Offer's snapshot of this shape,
// distinguished by chargeType.
//
// The spec is immutable: a new price is a new ServicePricing.
//
// +kubebuilder:validation:XValidation:rule="self.chargeType != 'Usage' || (has(self.metric) && has(self.pricingUnit) && has(self.rates))",message="Usage ServicePricing requires metric, pricingUnit, and rates"
// +kubebuilder:validation:XValidation:rule="self.chargeType == 'Usage' || !has(self.unitConversion)",message="unitConversion is only allowed for Usage ServicePricing"
// +kubebuilder:validation:XValidation:rule="self.chargeType != 'OneTime' || (has(self.amount) && has(self.trigger))",message="OneTime ServicePricing requires amount and trigger"
// +kubebuilder:validation:XValidation:rule="self.chargeType != 'Recurring' || (has(self.amount) && has(self.interval))",message="Recurring ServicePricing requires amount and interval"
type ServicePricingSpec struct {
	// ChargeType distinguishes Usage, OneTime, and Recurring pricing.
	//
	// +kubebuilder:validation:Required
	ChargeType ChargeType `json:"chargeType"`

	// ServiceRef is the canonical service identifier (e.g.
	// "compute.datumapis.com").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ServiceRef string `json:"serviceRef"`

	// Currency is the ISO 4217 currency code. USD only in v1.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^USD$`
	// +kubebuilder:default=USD
	Currency string `json:"currency"`

	// DisplayName is a human-readable label for invoices and portals.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName,omitempty"`

	// Metric is the meterName of the MeterDefinition a Usage pricing
	// selects (e.g. "compute.datumapis.com/instance/cpu-allocated").
	// Required when chargeType is Usage. The meter must exist and not be
	// Retired, and serviceRef must name the service that owns it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	Metric string `json:"metric,omitempty"`

	// PricingUnit is a human-readable billing unit label (e.g. "vcpu",
	// "gib"). Required when chargeType is Usage. Does not need to be the
	// literal UCUM unit string of the meter.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=64
	PricingUnit string `json:"pricingUnit,omitempty"`

	// UnitConversion converts the meter's usage into pricingUnit before the
	// rates apply, for a metric measured in a different unit than it is
	// priced in (e.g. bytes priced per GiB). Omitted when the metric is
	// already measured in pricingUnit. Usage charges only.
	//
	// +kubebuilder:validation:Optional
	UnitConversion *UnitConversion `json:"unitConversion,omitempty"`

	// Rates is the ordered list of rate entries for Usage charges.
	// Required when chargeType is Usage.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Rates []PricingRate `json:"rates,omitempty"`

	// Amount is the fixed USD decimal string for OneTime and Recurring
	// charges.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)(\.\d+)?$`
	Amount string `json:"amount,omitempty"`

	// Trigger identifies when a OneTime charge fires.
	//
	// +kubebuilder:validation:Optional
	Trigger ChargeTrigger `json:"trigger,omitempty"`

	// Interval is the cadence for a Recurring charge.
	//
	// +kubebuilder:validation:Optional
	Interval ChargeInterval `json:"interval,omitempty"`
}

// UnitConversion converts usage measured in the meter's unit into the
// ServicePricing's pricingUnit: usage in pricingUnit = metered usage /
// meterUnitsPerPricingUnit. Rates and tier bounds stay in pricingUnit, and
// invoices show the converted quantity.
type UnitConversion struct {
	// MeterUnitsPerPricingUnit is how many of the meter's units make one
	// pricingUnit, as a positive decimal string (e.g. "1073741824" for a
	// meter in bytes priced per GiB, "1000" for millicore-seconds priced
	// per vCPU-second).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([1-9]\d*(\.\d+)?|0\.\d*[1-9]\d*)$`
	MeterUnitsPerPricingUnit string `json:"meterUnitsPerPricingUnit"`

	// Description explains the conversion to the people who maintain the
	// pricing (e.g. "Memory is metered in byte-seconds and priced per
	// GiB-second; 1 GiB = 2^30 bytes").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Description string `json:"description"`
}

// ServicePricingStatus defines the observed state of a ServicePricing.
type ServicePricingStatus struct {
	// Conditions represent the latest available observations of the
	// resource's state.
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

// ServicePricing is the Schema for the servicepricings API. One resource
// per price: a rate for one meter, or a fixed charge. Kept in GitOps and
// applied to milo-system.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="ChargeType",type=string,JSONPath=`.spec.chargeType`
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.spec.serviceRef`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
// +genclient
type ServicePricing struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new ServicePricing for a new price"
	Spec   ServicePricingSpec   `json:"spec,omitempty"`
	Status ServicePricingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServicePricingList contains a list of ServicePricing.
type ServicePricingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServicePricing `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServicePricing{}, &ServicePricingList{})
}
