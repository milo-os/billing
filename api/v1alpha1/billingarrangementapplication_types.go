// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationPaymentOption is how an applicant would like to pay. It
// records what was asked for; what staff grant is the
// BillingArrangement that approves the application. Sponsored access is
// intentionally absent until it can be granted (as a credit).
// +kubebuilder:validation:Enum=Invoice;NotSure
type ApplicationPaymentOption string

const (
	// ApplicationPaymentOptionInvoice asks to be billed by invoice, paid by
	// bank transfer, ACH or wire.
	ApplicationPaymentOptionInvoice ApplicationPaymentOption = "Invoice"

	// ApplicationPaymentOptionNotSure leaves the choice to the reviewer.
	ApplicationPaymentOptionNotSure ApplicationPaymentOption = "NotSure"
)

// ApplicantOrganizationType is the kind of organization applying.
// +kubebuilder:validation:Enum=Company;PartnerOrReseller;Nonprofit;EducationOrResearch;Other
type ApplicantOrganizationType string

const (
	ApplicantOrganizationTypeCompany             ApplicantOrganizationType = "Company"
	ApplicantOrganizationTypePartnerOrReseller   ApplicantOrganizationType = "PartnerOrReseller"
	ApplicantOrganizationTypeNonprofit           ApplicantOrganizationType = "Nonprofit"
	ApplicantOrganizationTypeEducationOrResearch ApplicantOrganizationType = "EducationOrResearch"
	ApplicantOrganizationTypeOther               ApplicantOrganizationType = "Other"
)

// EstimatedMonthlySpend is the applicant's estimate of their monthly
// spend, in the billing account's currency. It helps the reviewer set a
// credit limit.
// +kubebuilder:validation:Enum=Under500;From500To2500;From2500To10000;Over10000;NotSure
type EstimatedMonthlySpend string

const (
	EstimatedMonthlySpendUnder500        EstimatedMonthlySpend = "Under500"
	EstimatedMonthlySpendFrom500To2500   EstimatedMonthlySpend = "From500To2500"
	EstimatedMonthlySpendFrom2500To10000 EstimatedMonthlySpend = "From2500To10000"
	EstimatedMonthlySpendOver10000       EstimatedMonthlySpend = "Over10000"
	EstimatedMonthlySpendNotSure         EstimatedMonthlySpend = "NotSure"
)

// BillingArrangementApplicationPhase is where an application sits in
// review. Derived from spec.withdrawn and the staff decision recorded
// for it: a BillingArrangement whose spec.applicationRef names it
// (approved) or a BillingArrangementDecline (declined).
// +kubebuilder:validation:Enum=Pending;Approved;Declined;Withdrawn
type BillingArrangementApplicationPhase string

const (
	// BillingArrangementApplicationPhasePending indicates the application
	// is waiting for a staff decision.
	BillingArrangementApplicationPhasePending BillingArrangementApplicationPhase = "Pending"

	// BillingArrangementApplicationPhaseApproved indicates staff granted
	// terms with a BillingArrangement that references the application.
	BillingArrangementApplicationPhaseApproved BillingArrangementApplicationPhase = "Approved"

	// BillingArrangementApplicationPhaseDeclined indicates staff declined
	// the application with a BillingArrangementDecline.
	BillingArrangementApplicationPhaseDeclined BillingArrangementApplicationPhase = "Declined"

	// BillingArrangementApplicationPhaseWithdrawn indicates the customer
	// withdrew the application before a decision.
	BillingArrangementApplicationPhaseWithdrawn BillingArrangementApplicationPhase = "Withdrawn"
)

// BillingArrangementApplicationConditionDecided is True once the
// application has been approved, declined or withdrawn, with the phase
// as its reason, and False with reason Pending while it awaits review.
const BillingArrangementApplicationConditionDecided = "Decided"

// BillingArrangementApplicationSpec defines the desired state of a
// BillingArrangementApplication.
//
// Everything except spec.withdrawn is fixed once submitted: to change an
// application, withdraw it and apply again.
//
// +kubebuilder:validation:XValidation:rule="self.paymentOption != 'Invoice' || has(self.invoice)",message="invoice is required when paymentOption is Invoice"
// +kubebuilder:validation:XValidation:rule="self.paymentOption == 'Invoice' || !has(self.invoice)",message="invoice may only be set when paymentOption is Invoice"
// +kubebuilder:validation:XValidation:rule="self.billingAccountRef == oldSelf.billingAccountRef && self.paymentOption == oldSelf.paymentOption && self.organization == oldSelf.organization && self.situation == oldSelf.situation && self.authorized == oldSelf.authorized && has(self.invoice) == has(oldSelf.invoice) && (!has(self.invoice) || self.invoice == oldSelf.invoice) && has(self.applicant) == has(oldSelf.applicant) && (!has(self.applicant) || self.applicant == oldSelf.applicant)",message="an application can't be changed after it's submitted; withdraw it and apply again"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.withdrawn) || !oldSelf.withdrawn || (has(self.withdrawn) && self.withdrawn)",message="a withdrawn application can't be reopened; apply again instead"
type BillingArrangementApplicationSpec struct {
	// BillingAccountRef references the BillingAccount in the same
	// namespace that the applicant wants terms for.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// PaymentOption is how the applicant would like to pay.
	//
	// +kubebuilder:validation:Required
	PaymentOption ApplicationPaymentOption `json:"paymentOption"`

	// Organization describes who is applying.
	//
	// +kubebuilder:validation:Required
	Organization ApplicantOrganization `json:"organization"`

	// Situation is the applicant's own description of why they can't pay
	// by card and how their organization pays its vendors.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2000
	Situation string `json:"situation"`

	// Invoice carries the invoice details. Required when paymentOption is
	// Invoice, and not allowed otherwise.
	//
	// +kubebuilder:validation:Optional
	Invoice *ApplicationInvoiceDetails `json:"invoice,omitempty"`

	// Authorized confirms the applicant is authorized to request payment
	// terms on behalf of the organization. Must be true.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self",message="the applicant must confirm they're authorized to request payment terms for the organization"
	Authorized bool `json:"authorized"`

	// Applicant is who submitted the application. Set by the admission
	// webhook from the authenticated user on create; any value supplied
	// by the client is replaced.
	//
	// +kubebuilder:validation:Optional
	Applicant *Applicant `json:"applicant,omitempty"`

	// Withdrawn withdraws the application. It can only be set while the
	// application is pending, and can't be undone.
	//
	// +kubebuilder:validation:Optional
	Withdrawn bool `json:"withdrawn,omitempty"`
}

// ApplicantOrganization describes the organization applying for terms.
type ApplicantOrganization struct {
	// LegalName is the organization's formal (registered) name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	LegalName string `json:"legalName"`

	// Type is the kind of organization.
	//
	// +kubebuilder:validation:Required
	Type ApplicantOrganizationType `json:"type"`

	// Website is the organization's website, used to verify it.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://`
	Website string `json:"website"`
}

// ApplicationInvoiceDetails are the details an applicant gives when
// asking to pay by invoice.
type ApplicationInvoiceDetails struct {
	// EstimatedMonthlySpend is the applicant's spend estimate.
	//
	// +kubebuilder:validation:Required
	EstimatedMonthlySpend EstimatedMonthlySpend `json:"estimatedMonthlySpend"`

	// AccountsPayableEmail receives invoices in addition to the billing
	// contact.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=254
	AccountsPayableEmail string `json:"accountsPayableEmail"`

	// PurchaseOrderNumber to print on invoices.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=64
	PurchaseOrderNumber string `json:"purchaseOrderNumber,omitempty"`

	// AgreementReference identifies an MSA or order form already signed
	// with Datum, when there is one.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	AgreementReference string `json:"agreementReference,omitempty"`
}

// Applicant identifies the user who submitted an application.
type Applicant struct {
	// Username is the authenticated user's name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=256
	Username string `json:"username"`

	// UID is the authenticated user's unique identifier.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	UID string `json:"uid,omitempty"`
}

// BillingArrangementApplicationStatus defines the observed state of a
// BillingArrangementApplication.
type BillingArrangementApplicationStatus struct {
	// Phase is where the application sits in review.
	//
	// +kubebuilder:validation:Optional
	Phase BillingArrangementApplicationPhase `json:"phase,omitempty"`

	// DecidedAt is when staff approved or declined the application.
	//
	// +kubebuilder:validation:Optional
	DecidedAt *metav1.Time `json:"decidedAt,omitempty"`

	// ArrangementRef names the BillingArrangement that approved the
	// application. Its terms are published on the BillingAccount's
	// status.paymentArrangement while they're active.
	//
	// +kubebuilder:validation:Optional
	ArrangementRef *BillingArrangementRef `json:"arrangementRef,omitempty"`

	// DeclineMessage is the reviewer's message to the applicant when the
	// application was declined. Internal reviewer notes are never copied
	// here.
	//
	// +kubebuilder:validation:Optional
	DeclineMessage string `json:"declineMessage,omitempty"`

	// Conditions represent the latest available observations of the
	// application's state. See BillingArrangementApplicationConditionDecided.
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

// BillingArrangementRef references a BillingArrangement in the same
// namespace.
type BillingArrangementRef struct {
	// Name is the name of the BillingArrangement.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// BillingArrangementApplication is the Schema for the
// billingarrangementapplications API.
//
// A BillingArrangementApplication is a customer's request for payment
// terms on a billing account ("Apply for payment terms"). Customers
// create applications; staff decide them, approving with a
// BillingArrangement whose spec.applicationRef names the application or
// declining with a BillingArrangementDecline. Only one application per
// billing account can be pending at a time. While one is, the
// BillingAccount's PaymentReady condition reports ApplicationPending.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Option",type=string,JSONPath=`.spec.paymentOption`
// +kubebuilder:printcolumn:name="Applicant",type=string,JSONPath=`.spec.applicant.username`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type BillingArrangementApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingArrangementApplicationSpec   `json:"spec,omitempty"`
	Status BillingArrangementApplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingArrangementApplicationList contains a list of
// BillingArrangementApplication.
type BillingArrangementApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingArrangementApplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingArrangementApplication{}, &BillingArrangementApplicationList{})
}
