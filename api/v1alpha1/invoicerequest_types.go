// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InvoiceRequestMode says whether an InvoiceRequest only works out the
// invoice or also issues it.
//
// +kubebuilder:validation:Enum=Preview;Issue
type InvoiceRequestMode string

const (
	// InvoiceRequestModePreview reports what the account will be invoiced
	// next for its fixed charges, without creating anything. Before a
	// contract starts, that's its first invoice. Nothing reaches the
	// customer.
	InvoiceRequestModePreview InvoiceRequestMode = "Preview"

	// InvoiceRequestModeIssue creates the invoice now and sends it to the
	// customer, the same way as an invoice at the end of a billing cycle.
	// Providers that invoice fixed charges as soon as they're due, as
	// openmeter-provider does, have nothing to issue and fail it as
	// Unsupported.
	InvoiceRequestModeIssue InvoiceRequestMode = "Issue"
)

// InvoiceRequestPhase is where an InvoiceRequest is in its lifecycle.
//
// +kubebuilder:validation:Enum=Pending;Succeeded;Failed
type InvoiceRequestPhase string

const (
	// InvoiceRequestPhasePending means the invoicing provider hasn't
	// finished the request.
	InvoiceRequestPhasePending InvoiceRequestPhase = "Pending"

	// InvoiceRequestPhaseSucceeded means the provider finished the
	// request: status holds the invoice's lines, and for Issue, the
	// Invoice it created.
	InvoiceRequestPhaseSucceeded InvoiceRequestPhase = "Succeeded"

	// InvoiceRequestPhaseFailed means the provider couldn't finish the
	// request. The Completed condition says why.
	InvoiceRequestPhaseFailed InvoiceRequestPhase = "Failed"
)

// InvoiceRequestConditionCompleted is True once the invoicing provider has
// finished an InvoiceRequest, successfully or not. Reasons include
// Succeeded, NothingToInvoice (Issue with no charges waiting), Unsupported
// (the provider can't invoice on request) and Failed.
const InvoiceRequestConditionCompleted = "Completed"

// Reasons for the InvoiceRequest Completed condition.
const (
	InvoiceRequestReasonSucceeded        = "Succeeded"
	InvoiceRequestReasonNothingToInvoice = "NothingToInvoice"
	InvoiceRequestReasonUnsupported      = "Unsupported"
	InvoiceRequestReasonFailed           = "Failed"
)

// InvoiceRequestSpec defines the desired state of an InvoiceRequest.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type InvoiceRequestSpec struct {
	// BillingAccountRef references the BillingAccount to invoice, in the
	// same namespace.
	//
	// +kubebuilder:validation:Required
	BillingAccountRef BillingAccountRef `json:"billingAccountRef"`

	// Mode is Preview to work out the invoice without creating it, or
	// Issue to create it and send it to the customer.
	//
	// +kubebuilder:validation:Required
	Mode InvoiceRequestMode `json:"mode"`
}

// InvoiceRequestLine is one charge on a requested invoice.
type InvoiceRequestLine struct {
	// Description names the charge, as it appears on the invoice.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=512
	Description string `json:"description"`

	// Period is the service period the charge covers. Unset for a one-time
	// charge.
	//
	// +kubebuilder:validation:Optional
	Period *InvoicePeriod `json:"period,omitempty"`

	// Amount is the charge after discounts and before tax, as a decimal
	// string (e.g. "1500.00").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^-?[0-9]+(\.[0-9]+)?$`
	Amount string `json:"amount"`
}

// InvoiceRequestInvoiceRef references the Invoice an Issue request created.
type InvoiceRequestInvoiceRef struct {
	// Name is the name of the Invoice in the same namespace.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// InvoiceRequestStatus defines the observed state of an InvoiceRequest.
// Written only by the invoicing provider.
type InvoiceRequestStatus struct {
	// Phase is Pending until the provider finishes the request, then
	// Succeeded or Failed.
	//
	// +kubebuilder:validation:Optional
	Phase InvoiceRequestPhase `json:"phase,omitempty"`

	// CurrencyCode is the ISO 4217 currency of the amounts.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[A-Z]{3}$`
	CurrencyCode string `json:"currencyCode,omitempty"`

	// Lines are the invoice's charges.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	// +listType=atomic
	Lines []InvoiceRequestLine `json:"lines,omitempty"`

	// Total is the sum of the lines, before tax, as a decimal string.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^-?[0-9]+(\.[0-9]+)?$`
	Total string `json:"total,omitempty"`

	// InvoiceRef references the Invoice an Issue request created, once the
	// provider has recorded it.
	//
	// +kubebuilder:validation:Optional
	InvoiceRef *InvoiceRequestInvoiceRef `json:"invoiceRef,omitempty"`

	// CompletedAt is when the provider finished the request.
	//
	// +kubebuilder:validation:Optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Conditions represent the latest available observations of the
	// request. See InvoiceRequestConditionCompleted.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// invoicing provider.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// InvoiceRequest is the Schema for the invoicerequests API.
//
// An InvoiceRequest asks the invoicing provider about a billing account's
// fixed charges: the one-time and recurring charges of its Offer, such as
// a contract's setup fee and monthly fee. Usage isn't covered. Staff use a
// Preview to check a new contract's billing before it starts: it reports
// the first invoice's lines without creating anything. An Issue asks the
// provider to invoice the charges now, for providers that wait for the end
// of the billing cycle.
//
// Invoices themselves stay provider-written: an Issue request leads the
// provider to create one, which status.invoiceRef then names. Each
// request is owned by its billing account and is done once its phase is
// Succeeded or Failed; create a new one to ask again.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.billingAccountRef.name`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Total",type=string,JSONPath=`.status.total`
// +kubebuilder:printcolumn:name="Invoice",type=string,JSONPath=`.status.invoiceRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
// +genclient
type InvoiceRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InvoiceRequestSpec   `json:"spec,omitempty"`
	Status InvoiceRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InvoiceRequestList contains a list of InvoiceRequest.
type InvoiceRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InvoiceRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InvoiceRequest{}, &InvoiceRequestList{})
}
