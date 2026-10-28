// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreditGrantPhase represents the lifecycle state of a CreditGrant.
// +kubebuilder:validation:Enum=Pending;Active;Expired;Voided;Failed
type CreditGrantPhase string

const (
	// CreditGrantPhasePending indicates the grant is waiting for the provider
	// to apply it (e.g. waiting for the customer to exist in the provider).
	CreditGrantPhasePending CreditGrantPhase = "Pending"

	// CreditGrantPhaseActive indicates the grant has been applied and is
	// available to draw down.
	CreditGrantPhaseActive CreditGrantPhase = "Active"

	// CreditGrantPhaseExpired indicates the grant reached its expiration time
	// and any remaining balance has been forfeited.
	CreditGrantPhaseExpired CreditGrantPhase = "Expired"

	// CreditGrantPhaseVoided indicates the grant was voided and any remaining
	// balance has been forfeited.
	CreditGrantPhaseVoided CreditGrantPhase = "Voided"

	// CreditGrantPhaseFailed indicates the provider was unable to apply the grant.
	CreditGrantPhaseFailed CreditGrantPhase = "Failed"
)

// CreditGrantReason represents the business reason for granting a credit.
// +kubebuilder:validation:Enum=Promotional;Sponsorship;Goodwill
type CreditGrantReason string

const (
	// CreditGrantReasonPromotional is for promotional or marketing credits,
	// including welcome credits.
	CreditGrantReasonPromotional CreditGrantReason = "Promotional"

	// CreditGrantReasonSponsorship is for sponsored access (nonprofits, education, partners).
	CreditGrantReasonSponsorship CreditGrantReason = "Sponsorship"

	// CreditGrantReasonGoodwill is for corrections or goodwill accommodations after incidents.
	CreditGrantReasonGoodwill CreditGrantReason = "Goodwill"
)

const (
	// CreditGrantConditionReady is set on CreditGrant by the billing provider
	// once the grant has been processed.
	CreditGrantConditionReady = "Ready"

	// CreditGrantDefaultLabel marks signup credit grants created by service defaults.
	CreditGrantDefaultLabel = "services.miloapis.com/credit-grant-default"
)

// CreditGrantSpec defines the desired state of a CreditGrant.
//
// A CreditGrant is mostly immutable once created. Only voided may change,
// and only from false to true. To change a grant, void it and create a new one.
//
// +kubebuilder:validation:XValidation:rule="self.billingAccountRef == oldSelf.billingAccountRef && self.amount == oldSelf.amount && self.currency == oldSelf.currency && self.reason == oldSelf.reason && self.description == oldSelf.description && has(self.expiresAfter) == has(oldSelf.expiresAfter) && (!has(self.expiresAfter) || self.expiresAfter == oldSelf.expiresAfter)",message="credit grant spec is immutable except voided"
// +kubebuilder:validation:XValidation:rule="!oldSelf.voided || self.voided",message="a voided credit grant cannot be unvoided"
type CreditGrantSpec struct {
	// BillingAccountRef references the BillingAccount this credit grant
	// belongs to. The BillingAccount must reside in the same namespace.
	// Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="billingAccountRef is immutable"
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// Amount is the dollar amount of credit granted, expressed as a positive
	// decimal string (e.g. "500.00"). Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([1-9][0-9]*(\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="amount is immutable"
	Amount string `json:"amount"`

	// Currency is the ISO 4217 currency code for the grant. USD only.
	// Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^USD$`
	// +kubebuilder:default=USD
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="currency is immutable"
	Currency string `json:"currency"`

	// Reason is the business reason for the grant. Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Promotional;Sponsorship;Goodwill
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="reason is immutable"
	Reason CreditGrantReason `json:"reason"`

	// Description explains why the credit was granted. Free text, required.
	// Immutable once set.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="description is immutable"
	Description string `json:"description"`

	// ExpiresAfter is an optional duration after the effective time when
	// the grant expires. Omitted means the grant never expires.
	// Immutable once set.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="expiresAfter is immutable"
	ExpiresAfter *metav1.Duration `json:"expiresAfter,omitempty"`

	// Voided marks the grant as voided. One-way: changing from false to
	// true voids what is left of the credit in the billing provider's ledger.
	// Credit already used stays used. A voided grant cannot be unvoided.
	//
	// +kubebuilder:default=false
	// +kubebuilder:validation:Optional
	Voided bool `json:"voided"`
}

// CreditGrantStatus defines the observed state of a CreditGrant.
// Populated exclusively by the billing provider.
type CreditGrantStatus struct {
	// Phase represents the current lifecycle phase of the grant.
	//
	// +kubebuilder:validation:Optional
	Phase CreditGrantPhase `json:"phase,omitempty"`

	// EffectiveAt is when the grant became active in the billing provider's ledger.
	//
	// +kubebuilder:validation:Optional
	EffectiveAt *metav1.Time `json:"effectiveAt,omitempty"`

	// ExpiresAt is when the grant expires, computed from effectiveAt and spec.expiresAfter.
	//
	// +kubebuilder:validation:Optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Conditions represent the latest available observations of the grant's state.
	// See CreditGrantConditionReady.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// reconciling provider controller.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// CreditGrant is the Schema for the creditgrants API.
//
// A CreditGrant represents a dollar amount of credit given to a billing
// account. Usage draws down credits before anything is charged to a payment
// method. The billing provider maintains the balance in its ledger; Milo
// holds no balance on the CreditGrant resource.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Amount",type=string,JSONPath=`.spec.amount`
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=`.spec.currency`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.spec.reason`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Voided",type=boolean,JSONPath=`.spec.voided`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type CreditGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CreditGrantSpec   `json:"spec"`
	Status CreditGrantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CreditGrantList contains a list of CreditGrant.
type CreditGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CreditGrant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CreditGrant{}, &CreditGrantList{})
}
