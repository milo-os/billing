// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BillingEntitlementConditionOfferAssigned is True when the referenced
// Offer exists and is in the GA launch stage.
const BillingEntitlementConditionOfferAssigned = "OfferAssigned"

// BillingEntitlementSpec defines the desired state of a BillingEntitlement.
//
// Exactly one active BillingEntitlement exists per billing account. It
// binds the account to a published Offer. Distinct from the per-project
// ServiceEntitlement in service-catalog.
//
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.contract) || has(self.contract)",message="a contract can't be removed"
type BillingEntitlementSpec struct {
	// BillingAccountRef references the BillingAccount in the same
	// namespace.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// OfferRef references a cluster-scoped published (GA) Offer.
	//
	// +kubebuilder:validation:Required
	OfferRef OfferReference `json:"offerRef"`

	// Contract holds the terms of a negotiated contract, such as a bare
	// metal customer's. Without it, the account's charges start when the
	// entitlement is created and bill on the 1st of each month.
	//
	// A contract can be added to an existing entitlement, usually together
	// with offerRef, to move an account onto it. The account is billed as
	// before until the contract starts, and by offerRef from then on. Once
	// set, a contract can't be removed.
	//
	// After a contract starts, changing offerRef amends it: billing
	// providers apply the new Offer from the account's next billing date,
	// so the current month is billed as agreed.
	//
	// +kubebuilder:validation:Optional
	Contract *BillingContract `json:"contract,omitempty"`
}

// ContractBillingDay is the day of the month a contract bills on.
//
// +kubebuilder:validation:Enum=StartDate;InvoiceDay
type ContractBillingDay string

const (
	// ContractBillingDayStartDate bills on the day the contract starts, so
	// every period is a full one.
	ContractBillingDayStartDate ContractBillingDay = "StartDate"
	// ContractBillingDayInvoiceDay bills on the account's invoice day, so
	// the first period runs from the start to it and is prorated.
	ContractBillingDayInvoiceDay ContractBillingDay = "InvoiceDay"
)

// BillingContract holds the terms of a negotiated contract.
//
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.billingDay) ? oldSelf.billingDay : 'StartDate') == (has(self.billingDay) ? self.billingDay : 'StartDate')",message="contract.billingDay is immutable"
type BillingContract struct {
	// StartsAt is when the contract starts, at midnight UTC. The Offer's
	// charges start on this day, so a one-time setup fee is charged then,
	// and, unless billingDay is InvoiceDay, monthly charges bill on this day
	// of each month: a contract starting on the 18th bills on the 18th, and
	// one starting on the 29th, 30th or 31st bills on the last day of
	// shorter months. When the
	// contract is set, it may be today or up to a year ahead, but not in
	// the past: billing providers can't start charging retroactively.
	// Immutable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="contract.startsAt is immutable"
	StartsAt metav1.Time `json:"startsAt"`

	// BillingDay is the day of the month the contract bills on.
	//
	// StartDate, the default, bills on the day it starts, so every period
	// is a full one: a contract starting on the 15th bills the 15th to the
	// 15th.
	//
	// InvoiceDay bills on the account's invoice day (its payment terms'
	// invoiceDayOfMonth, the 1st under the standard terms), like an account
	// without a contract. The first period runs from the start to the next
	// invoice day, and its recurring fees are prorated: a monthly fee on a
	// contract starting on the 15th is charged for the 15th to the 1st,
	// then for whole months. Quarterly and annual fees bill on the invoice
	// day of January, April, July and October, and of January, so their
	// first period runs to the next of those. A fee with periods runs for
	// that many whole periods after the prorated one.
	//
	// Immutable.
	//
	// +kubebuilder:validation:Optional
	BillingDay ContractBillingDay `json:"billingDay,omitempty"`
}

// BillingEntitlementStatus defines the observed state of a BillingEntitlement.
type BillingEntitlementStatus struct {
	// Conditions represent the latest available observations of the
	// entitlement's state.
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

// BillingEntitlement is the Schema for the billingentitlements API.
// Billing-account-scoped binding of an account to exactly one active Offer.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="BillingAccount",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Offer",type=string,JSONPath=`.spec.offerRef.name`
// +kubebuilder:printcolumn:name="Contract Start",type=date,JSONPath=`.spec.contract.startsAt`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +genclient
type BillingEntitlement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingEntitlementSpec   `json:"spec,omitempty"`
	Status BillingEntitlementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingEntitlementList contains a list of BillingEntitlement.
type BillingEntitlementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingEntitlement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingEntitlement{}, &BillingEntitlementList{})
}
