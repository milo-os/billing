// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"maps"
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
