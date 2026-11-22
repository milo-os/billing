// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChargePhase is where a Charge is in its lifecycle.
//
// +kubebuilder:validation:Enum=Pending;Scheduled;Invoiced;Failed
type ChargePhase string

const (
	// ChargePhasePending is a charge accepted but not yet on an invoice in
	// the billing provider.
	ChargePhasePending ChargePhase = "Pending"
	// ChargePhaseScheduled is a charge on the account's next invoice (or,
	// for invoice now, on a draft about to be issued). status.invoiceAt
	// says when it's invoiced.
	ChargePhaseScheduled ChargePhase = "Scheduled"
	// ChargePhaseInvoiced is a charge on an issued invoice, named by
	// status.invoiceRef.
	ChargePhaseInvoiced ChargePhase = "Invoiced"
	// ChargePhaseFailed is a charge the billing provider refused. The
	// reason is on the Ready condition. Staff remove it and add a
	// corrected one.
	ChargePhaseFailed ChargePhase = "Failed"
)

// ChargeInvoiceTiming says when a Charge is invoiced.
//
// +kubebuilder:validation:Enum=NextInvoice;Now
type ChargeInvoiceTiming string

const (
	// ChargeInvoiceNext puts the charge on the invoice the account gets at
	// the end of its current billing period.
	ChargeInvoiceNext ChargeInvoiceTiming = "NextInvoice"
	// ChargeInvoiceNow invoices the charge on its own straight away.
	ChargeInvoiceNow ChargeInvoiceTiming = "Now"
)

const (
	// ChargeConditionReady is True once the billing provider has the
	// charge on an invoice. Reasons include Scheduled, Invoiced,
	// WaitingForProvider, WaitingForCustomer, WaitingForInvoiceWriter and
	// Refused.
	ChargeConditionReady = "Ready"

	// ChargeAddedByAnnotation records the username of whoever added the
	// charge. The defaulting webhook sets it from the request, and the
	// invoice writer copies it onto the invoice line's metadata.
	ChargeAddedByAnnotation = "billing.miloapis.com/added-by"

	// ChargePurposeLabel, set to ChargePurposeCustomCharge on a
	// ServicePricing, marks the pricing whose product custom charges use.
	// The staff portal hides it from catalog pickers.
	ChargePurposeLabel = "billing.miloapis.com/purpose"
	// ChargePurposeCustomCharge is ChargePurposeLabel's value for the
	// custom charge pricing.
	ChargePurposeCustomCharge = "custom-charge"
)

// ChargeSpec is one charge staff add to a billing account: a catalog item
// or a custom one, with a quantity, a service period and a note.
//
// The spec is immutable. To change a charge, remove it and add another.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="a charge can't be changed; remove it and add another"
// +kubebuilder:validation:XValidation:rule="has(self.servicePricingRef) || (has(self.description) && has(self.unitAmount))",message="a custom charge (no servicePricingRef) needs a description and a unitAmount"
// +kubebuilder:validation:XValidation:rule="!has(self.tax) || !has(self.tax.taxable) || self.tax.taxable || (has(self.note) && size(self.note) > 0)",message="a charge that isn't taxable needs a note saying why"
type ChargeSpec struct {
	// BillingAccountRef names the BillingAccount, in this namespace, the
	// charge is billed to.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// ServicePricingRef names the catalog item: a OneTime or Recurring
	// ServicePricing. Its displayName, amount and tax are the defaults for
	// description, unitAmount and tax, and the invoice line counts as its
	// product in reports, at whatever price. Omitted, this is a custom
	// item, which uses the custom charge product.
	//
	// +kubebuilder:validation:Optional
	ServicePricingRef *ChargeServicePricingRef `json:"servicePricingRef,omitempty"`

	// Description is what the invoice calls the charge. Defaulted from the
	// pricing's displayName. It's printed on the invoice, so it never
	// carries the SKU.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Description string `json:"description,omitempty"`

	// Quantity is how many are charged: a positive decimal with up to two
	// decimal places (e.g. "2", "3.5").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([1-9][0-9]{0,8}(\.[0-9]{1,2})?|0\.(0[1-9]|[1-9][0-9]?))$`
	Quantity string `json:"quantity"`

	// UnitAmount is the price of one, before any discount: a decimal with
	// up to two decimal places, zero or more (e.g. "300.00"). Defaulted
	// from the pricing's amount; staff may change it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`
	UnitAmount string `json:"unitAmount,omitempty"`

	// Currency is the ISO 4217 currency of the amounts. USD only.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^USD$`
	// +kubebuilder:default=USD
	Currency string `json:"currency,omitempty"`

	// Tax is how the charge is taxed. Defaulted from the pricing's tax.
	// Omitted on a custom charge, the account's default tax code applies.
	//
	// +kubebuilder:validation:Optional
	Tax *PricingTax `json:"tax,omitempty"`

	// DiscountPercent is a percentage off this charge, more than 0 and up
	// to 100, with up to two decimal places (e.g. "10"). The discount must
	// come to whole cents.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^(100(\.0{1,2})?|[1-9]?[0-9](\.[0-9]{1,2})?)$`
	DiscountPercent string `json:"discountPercent,omitempty"`

	// ServicePeriod is the dates the charge is for: the invoice line's
	// service period. Defaulted to the account's current billing period.
	//
	// +kubebuilder:validation:Optional
	ServicePeriod *ServicePeriod `json:"servicePeriod,omitempty"`

	// Note is for staff and finance, kept on the invoice line's metadata.
	// It's never printed on the invoice or shown to the customer.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=500
	Note string `json:"note,omitempty"`

	// Invoice is when the charge is invoiced: NextInvoice, with the rest
	// of the account's current billing period, or Now, on an invoice of
	// its own.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=NextInvoice
	Invoice ChargeInvoiceTiming `json:"invoice,omitempty"`
}

// ChargeServicePricingRef names the ServicePricing a charge is for.
type ChargeServicePricingRef struct {
	// Name of the ServicePricing.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Namespace of the ServicePricing.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=milo-system
	Namespace string `json:"namespace,omitempty"`
}

// ServicePeriod is the dates a charge is for, as UTC days. Both ends are
// included, so a single day has start equal to end.
//
// +kubebuilder:validation:XValidation:rule="self.end >= self.start",message="the service period can't end before it starts"
type ServicePeriod struct {
	// Start is the first day, as YYYY-MM-DD.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$`
	Start string `json:"start"`

	// End is the last day, as YYYY-MM-DD.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$`
	End string `json:"end"`
}

// ChargeInvoiceRef names the Invoice a charge was billed on.
type ChargeInvoiceRef struct {
	// Name of the Invoice, in the charge's namespace.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ChargeStatus is the observed state of a Charge. Amounts are decimal
// strings in the currency.
type ChargeStatus struct {
	// Phase is where the charge is: Pending, Scheduled, Invoiced or Failed.
	//
	// +kubebuilder:validation:Optional
	Phase ChargePhase `json:"phase,omitempty"`

	// Amount is quantity × unitAmount, before any discount.
	//
	// +kubebuilder:validation:Optional
	Amount string `json:"amount,omitempty"`

	// AmountDue is the amount after any discount: what's billed before
	// tax.
	//
	// +kubebuilder:validation:Optional
	AmountDue string `json:"amountDue,omitempty"`

	// ListUnitAmount is the pricing's own amount, when the charge's
	// unitAmount differs from it.
	//
	// +kubebuilder:validation:Optional
	ListUnitAmount string `json:"listUnitAmount,omitempty"`

	// InvoiceAt is when the billing provider invoices the charge.
	//
	// +kubebuilder:validation:Optional
	InvoiceAt *metav1.Time `json:"invoiceAt,omitempty"`

	// InvoiceRef names the Invoice the charge was billed on, once it's
	// issued.
	//
	// +kubebuilder:validation:Optional
	InvoiceRef *ChargeInvoiceRef `json:"invoiceRef,omitempty"`

	// Conditions are the latest observations of the charge. See
	// ChargeConditionReady.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the generation billing last worked out the
	// amounts for.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// Charge is a one-off charge staff add to a billing account: a catalog
// item or a custom one, with a quantity, a service period, an optional
// discount and a staff note. It goes on the account's next invoice, or on
// an invoice of its own. Only staff can read charges, because of the note.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=`.spec.description`
// +kubebuilder:printcolumn:name="Amount",type=string,JSONPath=`.status.amountDue`
// +kubebuilder:printcolumn:name="From",type=string,JSONPath=`.spec.servicePeriod.start`
// +kubebuilder:printcolumn:name="To",type=string,JSONPath=`.spec.servicePeriod.end`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type Charge struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ChargeSpec   `json:"spec"`
	Status ChargeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ChargeList contains a list of Charge.
type ChargeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Charge `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Charge{}, &ChargeList{})
}
