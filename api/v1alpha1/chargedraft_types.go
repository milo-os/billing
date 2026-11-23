// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChargeDraftSpec is charges staff are preparing for a billing account and
// haven't sent: one or more lines, and when they'll be invoiced. Unlike a
// Charge, a draft can be changed until it's sent. Sending it adds a Charge
// for each line, in one invoice group when they're invoiced now, and
// removes the draft. Nothing in a draft is billed.
type ChargeDraftSpec struct {
	// BillingAccountRef names the BillingAccount, in this namespace, the
	// charges are for.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="billingAccountRef can't be changed"
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// Lines are the charges, in the order the invoice lists them.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	Lines []ChargeDraftLine `json:"lines"`

	// Invoice is when the charges will be invoiced once sent: NextInvoice,
	// with the rest of the account's current billing period, or Now, together
	// on an invoice of their own.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=NextInvoice
	Invoice ChargeInvoiceTiming `json:"invoice,omitempty"`
}

// ChargeDraftLine is one charge in a draft: what a Charge's spec holds for
// it, without the account or timing, which the draft gives for every line.
// Billing fills in what's left out, as it does for a Charge, when the
// draft is sent.
//
// +kubebuilder:validation:XValidation:rule="has(self.servicePricingRef) || (has(self.description) && has(self.unitAmount))",message="a custom charge (no servicePricingRef) needs a description and a unitAmount"
// +kubebuilder:validation:XValidation:rule="!has(self.tax) || !has(self.tax.taxable) || self.tax.taxable || (has(self.note) && size(self.note) > 0)",message="a charge that isn't taxable needs a note saying why"
type ChargeDraftLine struct {
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
}

// ChargeSpec is the Charge the line becomes for billing account account,
// invoiced at invoice.
func (l ChargeDraftLine) ChargeSpec(account string, invoice ChargeInvoiceTiming) ChargeSpec {
	return ChargeSpec{
		BillingAccountRef: BillingAccountRef{Name: account},
		ServicePricingRef: l.ServicePricingRef,
		Description:       l.Description,
		Quantity:          l.Quantity,
		UnitAmount:        l.UnitAmount,
		Currency:          l.Currency,
		Tax:               l.Tax,
		DiscountPercent:   l.DiscountPercent,
		ServicePeriod:     l.ServicePeriod,
		Note:              l.Note,
		Invoice:           invoice,
	}
}

// ChargeDraft is charges staff are preparing for a billing account, to
// check and send later: several lines, which can be changed, previewed
// with an InvoiceRequest, and sent as Charges. Only staff can read drafts,
// because of their notes.
//
// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Invoice",type=string,JSONPath=`.spec.invoice`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type ChargeDraft struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ChargeDraftSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ChargeDraftList contains a list of ChargeDraft.
type ChargeDraftList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChargeDraft `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ChargeDraft{}, &ChargeDraftList{})
}
