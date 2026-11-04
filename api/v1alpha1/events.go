// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"strings"
	"time"
)

// Event reasons for the billing activity timeline.
//
// Billing and the providers that serve it record these as Kubernetes Events
// on the resource they describe, and billing's ActivityPolicies match on
// them (config/components/activity-policies). Reasons are vendor-agnostic in
// the same way Invoice is: an event says what happened to a bill, never
// which vendor API reported it.
//
// An Event carries its detail in annotations with the EventAnnotationPrefix
// (see the EventAnnotation* keys), not in its message. Policies render the
// summary from those annotations, so the wording, the date format and what a
// customer is shown stay in billing. The message is a plain fallback for
// kubectl. Neither may contain raw vendor messages, vendor identifiers or
// decline codes.
//
// An Event reaches the owning organization's timeline only when it carries
// the scope annotations from OrganizationScopeAnnotations. Events without
// them stay on the platform timeline, which only staff can read; that is how
// operational events such as InvoiceCurrencyMismatch are kept staff-only.
//
// Emitters record each transition once. Retried webhooks and requeues must
// not record a second event for a transition already recorded.
const (
	// EventReasonInvoiceIssued is recorded on an Invoice when it is first
	// issued with phase Open. Annotations: billing account, period start,
	// currency, amount (the total), due date.
	EventReasonInvoiceIssued = "InvoiceIssued"

	// EventReasonInvoicePaymentSucceeded is recorded on an Invoice when it
	// moves to Paid. Annotations: billing account, period start, currency,
	// amount (the total).
	EventReasonInvoicePaymentSucceeded = "InvoicePaymentSucceeded"

	// EventReasonInvoicePaymentFailed is recorded on an Invoice each time a
	// charge attempt is declined or errors. Annotations: billing account,
	// period start, currency, amount (the amount attempted), decline
	// reason, and the next attempt when one is scheduled.
	EventReasonInvoicePaymentFailed = "InvoicePaymentFailed"

	// EventReasonInvoicePaymentActionRequired is recorded on an Invoice
	// when a charge needs the card holder to authenticate it (3-D Secure,
	// SCA). Annotations: billing account, period start, currency, amount.
	EventReasonInvoicePaymentActionRequired = "InvoicePaymentActionRequired"

	// EventReasonInvoicePartiallyPaid is recorded on an Invoice when a
	// payment leaves both amountPaid and amountDue above zero.
	// Annotations: billing account, period start, currency, amount paid,
	// amount due.
	EventReasonInvoicePartiallyPaid = "InvoicePartiallyPaid"

	// EventReasonInvoicePastDue is recorded on an Invoice when it moves to
	// PastDue. Annotations: billing account, period start, currency,
	// amount due.
	EventReasonInvoicePastDue = "InvoicePastDue"

	// EventReasonInvoiceVoided is recorded on an Invoice when it moves to
	// Void. Annotations: billing account, period start.
	EventReasonInvoiceVoided = "InvoiceVoided"

	// EventReasonInvoiceCurrencyMismatch is recorded on an Invoice when the
	// provider sets Ready=False with reason CurrencyMismatch. It is
	// operational, so it is recorded without scope annotations and only
	// staff see it. Annotations: billing account, period start, currency
	// (the provider's).
	EventReasonInvoiceCurrencyMismatch = "InvoiceCurrencyMismatch"
)

const (
	// EventReasonPaymentMethodAttached is recorded on a PaymentMethod by
	// its provider when the instrument is confirmed and chargeable
	// (phase Active). Annotations: billing account, and the card brand,
	// last four digits and expiry for a card.
	EventReasonPaymentMethodAttached = "PaymentMethodAttached"

	// EventReasonPaymentMethodFailed is recorded on a PaymentMethod by its
	// provider when setup is declined or fails (phase Failed).
	// Annotations: billing account, decline reason.
	EventReasonPaymentMethodFailed = "PaymentMethodFailed"

	// EventReasonPaymentMethodExpiringSoon is recorded on an Active card
	// by billing when its expiry month begins. The card still works this
	// month, but the next monthly invoice is charged after it expires.
	// Annotations: billing account, card brand, last four, expiry.
	EventReasonPaymentMethodExpiringSoon = "PaymentMethodExpiringSoon"

	// EventReasonPaymentMethodExpired is recorded on an Active card by
	// billing once its expiry month has ended. Annotations: billing
	// account, card brand, last four, expiry.
	EventReasonPaymentMethodExpired = "PaymentMethodExpired"
)

const (
	// EventReasonDefaultPaymentMethodChanged is recorded on a
	// BillingAccount by billing when billing itself sets the default
	// payment method (an account's only Active card becomes its default).
	// A person changing the default is recorded from the audit log.
	// Annotations: payment method, and the card brand and last four for a
	// card.
	EventReasonDefaultPaymentMethodChanged = "DefaultPaymentMethodChanged"

	// EventReasonInvoicingPastDue is recorded on a BillingAccount when
	// InvoicingReady becomes False with reason PastDue. Annotations: the
	// past-due invoice.
	EventReasonInvoicingPastDue = "InvoicingPastDue"

	// EventReasonInvoicingCurrent is recorded on a BillingAccount when
	// InvoicingReady returns to True after being PastDue.
	EventReasonInvoicingCurrent = "InvoicingCurrent"

	// EventReasonPaymentTermsEnded is recorded on a BillingArrangement when
	// its terms end on their own at spec.endsAt. A person ending terms is
	// recorded from the audit log instead. Annotations: billing account,
	// ends at.
	EventReasonPaymentTermsEnded = "PaymentTermsEnded"
)

// EventAnnotationPrefix prefixes every annotation billing reads from a
// timeline Event.
const EventAnnotationPrefix = "event.billing.miloapis.com/"

// Annotations that carry an Event's detail. Times are RFC 3339 in UTC and
// amounts are decimal strings in the format Invoice status uses (for
// example "482.19").
const (
	// EventAnnotationBillingAccount is the name of the BillingAccount the
	// event concerns, in the event's namespace.
	EventAnnotationBillingAccount = EventAnnotationPrefix + "billing-account"

	// EventAnnotationInvoice is the name of the Invoice the event concerns,
	// in the event's namespace.
	EventAnnotationInvoice = EventAnnotationPrefix + "invoice"

	// EventAnnotationPeriodStart is the start of the invoice's billing
	// period. Summaries name the period by its month ("October 2026").
	EventAnnotationPeriodStart = EventAnnotationPrefix + "period-start"

	// EventAnnotationCurrency is the ISO 4217 code of the amounts.
	EventAnnotationCurrency = EventAnnotationPrefix + "currency"

	// EventAnnotationAmount is the amount the event is about: the total
	// for an issued, paid or voided invoice, and the amount attempted for
	// a payment.
	EventAnnotationAmount = EventAnnotationPrefix + "amount"

	// EventAnnotationAmountPaid is the amount collected so far.
	EventAnnotationAmountPaid = EventAnnotationPrefix + "amount-paid"

	// EventAnnotationAmountDue is the amount still outstanding.
	EventAnnotationAmountDue = EventAnnotationPrefix + "amount-due"

	// EventAnnotationDueDate is when the invoice is due.
	EventAnnotationDueDate = EventAnnotationPrefix + "due-date"

	// EventAnnotationNextAttempt is when the provider will next try to
	// collect payment.
	EventAnnotationNextAttempt = EventAnnotationPrefix + "next-attempt"

	// EventAnnotationDeclineReason is why a payment or a payment method
	// setup failed, as a DeclineReason.
	EventAnnotationDeclineReason = EventAnnotationPrefix + "decline-reason"

	// EventAnnotationPaymentMethod is the name of the PaymentMethod the
	// event concerns, in the event's namespace.
	EventAnnotationPaymentMethod = EventAnnotationPrefix + "payment-method"

	// EventAnnotationCardBrand is the card network, as
	// PaymentMethodCardDetails.Brand reports it ("visa").
	EventAnnotationCardBrand = EventAnnotationPrefix + "card-brand"

	// EventAnnotationCardLast4 is the last four digits of the card.
	EventAnnotationCardLast4 = EventAnnotationPrefix + "card-last4"

	// EventAnnotationCardExpiry is the first instant of the card's expiry
	// month (a card expiring 11/2026 is "2026-11-01T00:00:00Z").
	EventAnnotationCardExpiry = EventAnnotationPrefix + "card-expiry"

	// EventAnnotationEndsAt is when payment terms ended.
	EventAnnotationEndsAt = EventAnnotationPrefix + "ends-at"
)

// DeclineReason is the normalized reason a payment or payment method setup
// failed. Providers map their vendor's decline codes onto it; anything they
// can't map is DeclineReasonDeclined (the issuer said no) or
// DeclineReasonProcessingError (the attempt didn't reach a decision).
type DeclineReason string

const (
	// DeclineReasonDeclined is a decline the issuer gave no more detail
	// about, or one that maps to no other reason.
	DeclineReasonDeclined DeclineReason = "Declined"

	// DeclineReasonInsufficientFunds means the account lacked the funds.
	DeclineReasonInsufficientFunds DeclineReason = "InsufficientFunds"

	// DeclineReasonExpiredCard means the card has expired.
	DeclineReasonExpiredCard DeclineReason = "ExpiredCard"

	// DeclineReasonAuthenticationRequired means the card holder has to
	// authenticate the payment (3-D Secure, SCA) and didn't.
	DeclineReasonAuthenticationRequired DeclineReason = "AuthenticationRequired"

	// DeclineReasonProcessingError means the attempt failed before the
	// issuer decided, for example a network or processor error.
	DeclineReasonProcessingError DeclineReason = "ProcessingError"
)

// Scope annotations Milo reads to place an Event on a tenant's activity
// timeline. They match go.miloapis.com/milo's events package.
const (
	ScopeTypeAnnotation = "platform.miloapis.com/scope.type"
	ScopeNameAnnotation = "platform.miloapis.com/scope.name"
)

// organizationEventNamespacePrefix is how Milo names an organization's
// namespace: organization-<organization name>.
const organizationEventNamespacePrefix = "organization-"

// OrganizationScopeAnnotations returns the scope annotations that put an
// Event about a resource in namespace on its organization's activity
// timeline. Controllers write Events as service accounts, which Milo
// doesn't scope, so without these an Event only reaches the platform
// timeline. It returns nil for a namespace that isn't an organization's.
func OrganizationScopeAnnotations(namespace string) map[string]string {
	org, ok := strings.CutPrefix(namespace, organizationEventNamespacePrefix)
	if !ok || org == "" {
		return nil
	}
	return map[string]string{
		ScopeTypeAnnotation: "Organization",
		ScopeNameAnnotation: org,
	}
}

// EventTime formats t the way timeline Event annotations carry times.
func EventTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
