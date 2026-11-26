// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"fmt"
	"maps"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// eventRecorderName is the component billing's timeline Events are
// reported by. The activity timeline shows it as the actor.
const eventRecorderName = "billing-controller"

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// recordTimelineEvent records a timeline Event on obj with the given detail
// annotations, scoped to obj's organization so its members see it. A nil
// recorder (unit tests that don't care about events) records nothing.
func recordTimelineEvent(
	recorder record.EventRecorder,
	obj client.Object,
	detail map[string]string,
	eventType, reason, message string,
) {
	if recorder == nil {
		return
	}
	annotations := billingv1alpha1.OrganizationScopeAnnotations(obj.GetNamespace())
	if annotations == nil {
		annotations = map[string]string{}
	}
	maps.Copy(annotations, detail)
	recorder.AnnotatedEventf(obj, annotations, eventType, reason, "%s", message)
}

// invoicingTransition returns the timeline event reason for a change in the
// InvoicingReady condition: InvoicingPastDue when it becomes False/PastDue,
// and InvoicingCurrent when it leaves PastDue for True. Any other change,
// including the first time the condition is set to anything but PastDue,
// records nothing.
func invoicingTransition(before, after *metav1.Condition) string {
	wasPastDue := isInvoicingPastDue(before)
	isPastDue := isInvoicingPastDue(after)
	switch {
	case isPastDue && !wasPastDue:
		return billingv1alpha1.EventReasonInvoicingPastDue
	case wasPastDue && after != nil && after.Status == metav1.ConditionTrue:
		return billingv1alpha1.EventReasonInvoicingCurrent
	}
	return ""
}

func isInvoicingPastDue(c *metav1.Condition) bool {
	return c != nil && c.Status == metav1.ConditionFalse && c.Reason == "PastDue"
}

// invoicingCondition returns a copy of the account's InvoicingReady
// condition, or nil.
func invoicingCondition(account *billingv1alpha1.BillingAccount) *metav1.Condition {
	c := apimeta.FindStatusCondition(account.Status.Conditions, billingv1alpha1.BillingAccountConditionInvoicingReady)
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// recordInvoicingTransition records InvoicingPastDue or InvoicingCurrent
// when InvoicingReady changed between before and the account's current
// status. pastDueInvoice is the invoice that makes the account past due.
// Call it only after the status write succeeded, so a conflict and retry
// can't record the same transition twice.
func recordInvoicingTransition(
	recorder record.EventRecorder,
	account *billingv1alpha1.BillingAccount,
	before *metav1.Condition,
	pastDueInvoice string,
) {
	after := invoicingCondition(account)
	switch invoicingTransition(before, after) {
	case billingv1alpha1.EventReasonInvoicingPastDue:
		var detail map[string]string
		if pastDueInvoice != "" {
			detail = map[string]string{billingv1alpha1.EventAnnotationInvoice: pastDueInvoice}
		}
		recordTimelineEvent(recorder, account, detail, corev1.EventTypeWarning,
			billingv1alpha1.EventReasonInvoicingPastDue, after.Message)
	case billingv1alpha1.EventReasonInvoicingCurrent:
		recordTimelineEvent(recorder, account, nil, corev1.EventTypeNormal,
			billingv1alpha1.EventReasonInvoicingCurrent, "Billing account has no past-due invoices.")
	}
}

// cardEventDetail returns the annotations that describe a card, or nil when
// the payment method isn't a card with details yet.
func cardEventDetail(pm *billingv1alpha1.PaymentMethod) map[string]string {
	if pm.Status.Details == nil || pm.Status.Details.Card == nil {
		return nil
	}
	card := pm.Status.Details.Card
	detail := map[string]string{
		billingv1alpha1.EventAnnotationCardBrand: card.Brand,
		billingv1alpha1.EventAnnotationCardLast4: card.Last4,
	}
	if card.ExpiryYear > 0 && card.ExpiryMonth >= 1 && card.ExpiryMonth <= 12 {
		detail[billingv1alpha1.EventAnnotationCardExpiry] = billingv1alpha1.EventTime(cardExpiryMonth(card))
	}
	return detail
}

// cardExpiryMonth is the first instant of the month a card expires in. The
// card works until the end of that month.
func cardExpiryMonth(card *billingv1alpha1.PaymentMethodCardDetails) time.Time {
	return time.Date(int(card.ExpiryYear), time.Month(card.ExpiryMonth), 1, 0, 0, 0, 0, time.UTC)
}

// contractTermStatus returns the account's published contract term and its
// ContractRenewalDue condition, copied, for comparing before and after a
// reconcile.
func contractTermStatus(account *billingv1alpha1.BillingAccount) (*billingv1alpha1.BillingAccountContractTerm, *metav1.Condition) {
	var term *billingv1alpha1.BillingAccountContractTerm
	if account.Status.Contract != nil {
		term = account.Status.Contract.Term.DeepCopy()
	}
	cond := apimeta.FindStatusCondition(account.Status.Conditions, billingv1alpha1.BillingAccountConditionContractRenewalDue)
	return term, cond.DeepCopy()
}

// recordContractTermTransition records the timeline events for a contract
// reaching a point in its term on its own: the renewal flag turning on, the
// term ending, a renewal and a stop. Each is told from the published term
// before and after, and only when time, not an edit, moved it there: staff
// changing the term are recorded from the audit log instead.
func recordContractTermTransition(
	recorder record.EventRecorder,
	account *billingv1alpha1.BillingAccount,
	before, after *billingv1alpha1.BillingAccountContractTerm,
	renewalBefore, renewalAfter *metav1.Condition,
) {
	if recorder == nil || before == nil || after == nil || before.AtEnd != after.AtEnd {
		return
	}
	renewed := after.Phase == billingv1alpha1.ContractTermPhaseInTerm &&
		before.Phase == billingv1alpha1.ContractTermPhaseInTerm &&
		after.Renewals > before.Renewals && after.CurrentTermStartsAt.Equal(&before.EndsAt)
	sameTerm := before.EndsAt.Equal(&after.EndsAt)

	switch {
	case renewed:
		recordTimelineEvent(recorder, account, map[string]string{
			billingv1alpha1.EventAnnotationMonths: strconv.Itoa(int(after.RenewMonths)),
			billingv1alpha1.EventAnnotationEndsAt: billingv1alpha1.EventTime(after.EndsAt.Time),
		}, corev1.EventTypeNormal, billingv1alpha1.EventReasonContractRenewed, fmt.Sprintf(
			"The contract renewed for %d months, until %s.", after.RenewMonths, formatEventDate(after.EndsAt.Time)))
	case before.Phase == billingv1alpha1.ContractTermPhaseInTerm &&
		after.Phase == billingv1alpha1.ContractTermPhaseMonthToMonth && sameTerm:
		recordTimelineEvent(recorder, account, map[string]string{
			billingv1alpha1.EventAnnotationEndsAt: billingv1alpha1.EventTime(after.EndsAt.Time),
		}, corev1.EventTypeNormal, billingv1alpha1.EventReasonContractTermEnded, fmt.Sprintf(
			"The contract's term ended on %s, and it carries on month to month.", formatEventDate(after.EndsAt.Time)))
	case before.Phase != billingv1alpha1.ContractTermPhaseStopped &&
		after.Phase == billingv1alpha1.ContractTermPhaseStopped &&
		before.StopsAt != nil && before.StopsAt.Equal(&after.EndsAt):
		recordTimelineEvent(recorder, account, map[string]string{
			billingv1alpha1.EventAnnotationEndsAt: billingv1alpha1.EventTime(after.EndsAt.Time),
		}, corev1.EventTypeNormal, billingv1alpha1.EventReasonContractStopped, fmt.Sprintf(
			"The contract ended on %s.", formatEventDate(after.EndsAt.Time)))
	}

	becameDue := renewalAfter != nil &&
		renewalAfter.Reason == billingv1alpha1.ContractRenewalDueReasonTermEndingSoon &&
		(renewed || (sameTerm && renewalBefore != nil && renewalBefore.Reason == billingv1alpha1.ContractRenewalDueReasonInTerm))
	if becameDue {
		detail := map[string]string{
			billingv1alpha1.EventAnnotationEndsAt:        billingv1alpha1.EventTime(after.EndsAt.Time),
			billingv1alpha1.EventAnnotationContractAtEnd: string(after.AtEnd),
		}
		if after.AtEnd == billingv1alpha1.ContractTermAtEndRenew {
			detail[billingv1alpha1.EventAnnotationMonths] = strconv.Itoa(int(after.RenewMonths))
		}
		// For staff only: no organization scope, so it reaches the platform
		// timeline and not the customer's.
		recorder.AnnotatedEventf(account, detail, corev1.EventTypeNormal, billingv1alpha1.EventReasonContractRenewalDue,
			"%s", renewalAfter.Message)
	}
}

// formatEventDate formats t as "18 April 2029", the way the activity
// timeline shows dates.
func formatEventDate(t time.Time) string {
	return t.UTC().Format("2 January 2006")
}
