// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

// ChargeType distinguishes how a ServicePricing is billed.
// +kubebuilder:validation:Enum=Usage;OneTime;Recurring
type ChargeType string

const (
	// ChargeTypeUsage is metered consumption multiplied by a rate.
	ChargeTypeUsage ChargeType = "Usage"

	// ChargeTypeOneTime is a fixed amount charged once at a defined trigger.
	ChargeTypeOneTime ChargeType = "OneTime"

	// ChargeTypeRecurring is a fixed amount charged each billing cycle.
	ChargeTypeRecurring ChargeType = "Recurring"
)

// ChargeTrigger identifies when a OneTime charge fires.
// +kubebuilder:validation:Enum=BillingAccountActivation
type ChargeTrigger string

const (
	// ChargeTriggerBillingAccountActivation fires when a billing account
	// first becomes active on the service.
	ChargeTriggerBillingAccountActivation ChargeTrigger = "BillingAccountActivation"
)

// ChargeInterval is the cadence for a Recurring charge.
// +kubebuilder:validation:Enum=monthly
type ChargeInterval string

const (
	// ChargeIntervalMonthly bills once per calendar month.
	ChargeIntervalMonthly ChargeInterval = "monthly"
)

// OfferLaunchStage is the lifecycle stage of an Offer.
// +kubebuilder:validation:Enum=Draft;GA
type OfferLaunchStage string

const (
	// OfferLaunchStageDraft indicates the Offer is mutable and not yet
	// assignable to billing accounts.
	OfferLaunchStageDraft OfferLaunchStage = "Draft"

	// OfferLaunchStageGA indicates the Offer is published. Rates are
	// snapshotted and the Offer is immutable except for its display-name
	// annotation.
	OfferLaunchStageGA OfferLaunchStage = "GA"
)

// DisplayNameAnnotation is the well-known annotation carrying a
// human-readable Offer name. Editable after GA without bumping the Offer
// version.
const DisplayNameAnnotation = "kubernetes.io/display-name"

// DefaultServicePricingNamespace is the namespace into which service-catalog
// fans out ServicePricing resources.
const DefaultServicePricingNamespace = "milo-system"

// DimensionMatch selects a rate by a single MeterDefinition dimension
// value. Multi-dimension matches are deferred.
type DimensionMatch struct {
	// Dimension is the label key declared on the monitored resource type
	// (e.g. "tier", "region", "model").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Dimension string `json:"dimension"`

	// Value is the dimension value this rate applies to.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Value string `json:"value"`
}

// PricingTierBand is a single graduated volume band. Aggregation for
// tier breaks is monthly at billing-account scope. Each band has a rate
// and an exclusive upper bound upTo in pricingUnit units. The last band
// omits upTo (open-ended).
type PricingTierBand struct {
	// UpTo is the exclusive upper bound of this band in pricingUnit units.
	// Omit on the last band for an open-ended range.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)(\.\d+)?$`
	UpTo string `json:"upTo,omitempty"`

	// Rate is the USD decimal string applied to usage within this band.
	// Zero ("0") is valid for free allowances.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)(\.\d+)?$`
	Rate string `json:"rate"`
}

// PricingRate is a single rate entry. Exactly one of Flat or Tiered must
// be set. An optional Match filters the rate by dimension value; the last
// unmatched entry is the default catch-all.
//
// +kubebuilder:validation:XValidation:rule="has(self.flat) != has(self.tiered)",message="exactly one of flat or tiered must be set"
type PricingRate struct {
	// Match optionally restricts this rate to a single dimension value.
	//
	// +kubebuilder:validation:Optional
	Match *DimensionMatch `json:"match,omitempty"`

	// Flat is a single decimal USD string multiplied by metered usage.
	// Mutually exclusive with Tiered.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)(\.\d+)?$`
	Flat string `json:"flat,omitempty"`

	// Tiered is an ordered list of graduated volume bands.
	// Mutually exclusive with Flat.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Tiered []PricingTierBand `json:"tiered,omitempty"`
}

// ServicePricingRef names a ServicePricing resource referenced by an Offer
// while in Draft. Namespace defaults to milo-system when omitted.
type ServicePricingRef struct {
	// Name is the metadata.name of the ServicePricing.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Namespace is the namespace of the ServicePricing. Defaults to
	// milo-system when empty.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace,omitempty"`
}

// ServicePricingSnapshot is an immutable copy of a ServicePricingSpec taken
// at Offer publish time, plus the source ServicePricing name.
type ServicePricingSnapshot struct {
	// Name is the metadata.name of the source ServicePricing.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Spec is a deep copy of the source ServicePricingSpec at publish time.
	//
	// +kubebuilder:validation:Required
	Spec ServicePricingSpec `json:"spec"`
}

// OfferReference names a cluster-scoped Offer.
type OfferReference struct {
	// Name of the Offer.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}
