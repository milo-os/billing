// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// recordedEvent is one Event a captureRecorder saw.
type recordedEvent struct {
	object      runtime.Object
	annotations map[string]string
	eventType   string
	reason      string
	message     string
}

// captureRecorder is a record.EventRecorder that keeps what it's given.
type captureRecorder struct {
	events []recordedEvent
}

func (c *captureRecorder) Event(object runtime.Object, eventType, reason, message string) {
	c.AnnotatedEventf(object, nil, eventType, reason, "%s", message)
}

func (c *captureRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...any) {
	c.AnnotatedEventf(object, nil, eventType, reason, messageFmt, args...)
}

func (c *captureRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...any) {
	c.events = append(c.events, recordedEvent{
		object:      object,
		annotations: annotations,
		eventType:   eventType,
		reason:      reason,
		message:     fmt.Sprintf(messageFmt, args...),
	})
}

func (c *captureRecorder) reasons() []string {
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e.reason)
	}
	return out
}

func eventScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func TestRecordTimelineEventScopesToOrganization(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace string
		want      map[string]string
	}{
		"organization namespace": {
			namespace: "organization-acme",
			want: map[string]string{
				billingv1alpha1.ScopeTypeAnnotation:           "Organization",
				billingv1alpha1.ScopeNameAnnotation:           "acme",
				billingv1alpha1.EventAnnotationBillingAccount: "acme",
			},
		},
		"other namespace stays on the platform timeline": {
			namespace: "default",
			want: map[string]string{
				billingv1alpha1.EventAnnotationBillingAccount: "acme",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &captureRecorder{}
			account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: tc.namespace}}
			recordTimelineEvent(rec, account, map[string]string{billingv1alpha1.EventAnnotationBillingAccount: "acme"},
				corev1.EventTypeNormal, "Reason", "message")
			if len(rec.events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(rec.events))
			}
			got := rec.events[0].annotations
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("annotations = %v, want %v", got, tc.want)
			}
		})
	}

	// A nil recorder records nothing and doesn't panic.
	recordTimelineEvent(nil, &billingv1alpha1.BillingAccount{}, nil, corev1.EventTypeNormal, "Reason", "message")
}

func TestInvoicingTransition(t *testing.T) {
	pastDue := &metav1.Condition{Status: metav1.ConditionFalse, Reason: "PastDue"}
	current := &metav1.Condition{Status: metav1.ConditionTrue, Reason: "Current"}
	noInvoices := &metav1.Condition{Status: metav1.ConditionTrue, Reason: "NoInvoicesYet"}
	pending := &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "PhasePending"}

	for name, tc := range map[string]struct {
		before, after *metav1.Condition
		want          string
	}{
		"current to past due":           {current, pastDue, billingv1alpha1.EventReasonInvoicingPastDue},
		"first condition is past due":   {nil, pastDue, billingv1alpha1.EventReasonInvoicingPastDue},
		"pending to past due":           {pending, pastDue, billingv1alpha1.EventReasonInvoicingPastDue},
		"still past due":                {pastDue, pastDue, ""},
		"past due to current":           {pastDue, current, billingv1alpha1.EventReasonInvoicingCurrent},
		"past due to no invoices":       {pastDue, noInvoices, billingv1alpha1.EventReasonInvoicingCurrent},
		"past due to pending":           {pastDue, pending, ""},
		"first condition is current":    {nil, current, ""},
		"current stays current":         {current, current, ""},
		"no invoices to current":        {noInvoices, current, ""},
		"pending to current":            {pending, current, ""},
		"past due condition disappears": {pastDue, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := invoicingTransition(tc.before, tc.after); got != tc.want {
				t.Errorf("invoicingTransition() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordInvoicingTransition(t *testing.T) {
	account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}
	account.Status.Conditions = []metav1.Condition{{
		Type:    billingv1alpha1.BillingAccountConditionInvoicingReady,
		Status:  metav1.ConditionFalse,
		Reason:  "PastDue",
		Message: `Invoice "acme-2026-10" is past due.`,
	}}

	rec := &captureRecorder{}
	recordInvoicingTransition(rec, account, &metav1.Condition{Status: metav1.ConditionTrue, Reason: "Current"}, "acme-2026-10")
	if len(rec.events) != 1 {
		t.Fatalf("recorded %v, want one InvoicingPastDue", rec.reasons())
	}
	e := rec.events[0]
	if e.reason != billingv1alpha1.EventReasonInvoicingPastDue || e.eventType != corev1.EventTypeWarning {
		t.Errorf("recorded %s %s, want Warning InvoicingPastDue", e.eventType, e.reason)
	}
	if got := e.annotations[billingv1alpha1.EventAnnotationInvoice]; got != "acme-2026-10" {
		t.Errorf("invoice annotation = %q, want acme-2026-10", got)
	}

	// Reconciling again with the condition unchanged records nothing.
	rec = &captureRecorder{}
	recordInvoicingTransition(rec, account, invoicingCondition(account), "acme-2026-10")
	if len(rec.events) != 0 {
		t.Errorf("recorded %v on an unchanged condition, want nothing", rec.reasons())
	}
}

func TestTermsEndedOnTheirOwn(t *testing.T) {
	endsAt := metav1.NewTime(time.Date(2027, 9, 30, 0, 0, 0, 0, time.UTC))
	arrangement := func(phase billingv1alpha1.BillingArrangementPhase, generation, observed int64, ends *metav1.Time) *billingv1alpha1.BillingArrangement {
		arr := &billingv1alpha1.BillingArrangement{ObjectMeta: metav1.ObjectMeta{Generation: generation}}
		arr.Spec.EndsAt = ends
		arr.Status.Phase = phase
		arr.Status.ObservedGeneration = observed
		return arr
	}
	for name, tc := range map[string]struct {
		arr   *billingv1alpha1.BillingArrangement
		phase billingv1alpha1.BillingArrangementPhase
		want  bool
	}{
		"active terms reach endsAt": {
			arrangement(billingv1alpha1.BillingArrangementPhaseActive, 1, 1, &endsAt), billingv1alpha1.BillingArrangementPhaseEnded, true,
		},
		"a person ends active terms": {
			arrangement(billingv1alpha1.BillingArrangementPhaseActive, 2, 1, &endsAt), billingv1alpha1.BillingArrangementPhaseEnded, false,
		},
		"already ended": {
			arrangement(billingv1alpha1.BillingArrangementPhaseEnded, 1, 1, &endsAt), billingv1alpha1.BillingArrangementPhaseEnded, false,
		},
		"scheduled terms withdrawn": {
			arrangement(billingv1alpha1.BillingArrangementPhaseScheduled, 1, 1, &endsAt), billingv1alpha1.BillingArrangementPhaseEnded, false,
		},
		"still active": {
			arrangement(billingv1alpha1.BillingArrangementPhaseActive, 1, 1, &endsAt), billingv1alpha1.BillingArrangementPhaseActive, false,
		},
		"never reconciled": {
			arrangement("", 1, 0, &endsAt), billingv1alpha1.BillingArrangementPhaseEnded, false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := termsEndedOnTheirOwn(tc.arr, tc.phase); got != tc.want {
				t.Errorf("termsEndedOnTheirOwn() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBillingArrangementReconcileRecordsTermsEnded(t *testing.T) {
	endsAt := time.Date(2027, 9, 30, 0, 0, 0, 0, time.UTC)
	arr := &billingv1alpha1.BillingArrangement{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "acme-terms",
			Namespace:         "organization-acme",
			Generation:        1,
			CreationTimestamp: metav1.NewTime(endsAt.AddDate(-1, 0, 0)),
		},
		Spec: billingv1alpha1.BillingArrangementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			EndsAt:            &metav1.Time{Time: endsAt},
		},
	}
	arr.Status.Phase = billingv1alpha1.BillingArrangementPhaseActive
	arr.Status.ObservedGeneration = 1

	c := fake.NewClientBuilder().WithScheme(eventScheme(t)).
		WithObjects(arr).WithStatusSubresource(arr).Build()
	rec := &captureRecorder{}
	r := &BillingArrangementReconciler{client: c, recorder: rec, now: func() time.Time { return endsAt.Add(time.Second) }}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: arr.Namespace, Name: arr.Name}}

	for range 2 {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.events) != 1 || rec.events[0].reason != billingv1alpha1.EventReasonPaymentTermsEnded {
		t.Fatalf("recorded %v, want one PaymentTermsEnded", rec.reasons())
	}
	got := rec.events[0].annotations
	if got[billingv1alpha1.EventAnnotationBillingAccount] != "acme" ||
		got[billingv1alpha1.EventAnnotationEndsAt] != "2027-09-30T00:00:00Z" ||
		got[billingv1alpha1.ScopeNameAnnotation] != "acme" {
		t.Errorf("annotations = %v", got)
	}
}

func activeCard(month, year int32) *billingv1alpha1.PaymentMethod {
	pm := &billingv1alpha1.PaymentMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "visa-4242", Namespace: "organization-acme"},
		Spec: billingv1alpha1.PaymentMethodSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			DisplayName:       "Visa",
		},
	}
	pm.Status.Phase = billingv1alpha1.PaymentMethodPhaseActive
	pm.Status.Details = &billingv1alpha1.PaymentMethodDetails{
		Type: "card",
		Card: &billingv1alpha1.PaymentMethodCardDetails{
			Brand: "visa", Last4: "4242", Country: "US", ExpiryMonth: month, ExpiryYear: year,
		},
	}
	return pm
}

func TestCardExpiryNotice(t *testing.T) {
	card := activeCard(11, 2026)
	pending := activeCard(11, 2026)
	pending.Status.Phase = billingv1alpha1.PaymentMethodPhasePending
	bank := activeCard(11, 2026)
	bank.Status.Details.Card = nil

	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }
	for name, tc := range map[string]struct {
		pm       *billingv1alpha1.PaymentMethod
		now      time.Time
		want     string
		wantNext time.Time
	}{
		"before the expiry month": {card, at(2026, 10, 15), "", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		"during the expiry month": {card, at(2026, 11, 3), billingv1alpha1.EventReasonPaymentMethodExpiringSoon, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		"after the expiry month":  {card, at(2026, 12, 1), billingv1alpha1.EventReasonPaymentMethodExpired, time.Time{}},
		"pending card":            {pending, at(2026, 12, 1), "", time.Time{}},
		"not a card":              {bank, at(2026, 12, 1), "", time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, next := cardExpiryNotice(tc.pm, tc.now)
			if got != tc.want || !next.Equal(tc.wantNext) {
				t.Errorf("cardExpiryNotice() = %q, %v; want %q, %v", got, next, tc.want, tc.wantNext)
			}
		})
	}
}

func TestReconcileCardExpiryRecordsEachNoticeOnce(t *testing.T) {
	pm := activeCard(11, 2026)
	c := fake.NewClientBuilder().WithScheme(eventScheme(t)).WithObjects(pm).Build()
	rec := &captureRecorder{}
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	r := &PaymentMethodReconciler{client: c, recorder: rec, now: func() time.Time { return now }}

	reconcileOnce := func() {
		t.Helper()
		var current billingv1alpha1.PaymentMethod
		if err := c.Get(context.Background(), client.ObjectKeyFromObject(pm), &current); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcileCardExpiry(context.Background(), &current); err != nil {
			t.Fatal(err)
		}
	}

	reconcileOnce()
	reconcileOnce()
	now = time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
	reconcileOnce()
	reconcileOnce()

	want := []string{billingv1alpha1.EventReasonPaymentMethodExpiringSoon, billingv1alpha1.EventReasonPaymentMethodExpired}
	if fmt.Sprint(rec.reasons()) != fmt.Sprint(want) {
		t.Fatalf("recorded %v, want %v", rec.reasons(), want)
	}
	got := rec.events[0].annotations
	for key, want := range map[string]string{
		billingv1alpha1.EventAnnotationBillingAccount: "acme",
		billingv1alpha1.EventAnnotationCardBrand:      "visa",
		billingv1alpha1.EventAnnotationCardLast4:      "4242",
		billingv1alpha1.EventAnnotationCardExpiry:     "2026-11-01T00:00:00Z",
		billingv1alpha1.ScopeTypeAnnotation:           "Organization",
	} {
		if got[key] != want {
			t.Errorf("annotation %s = %q, want %q", key, got[key], want)
		}
	}
}

func TestReconcileDefaultPaymentMethodRecordsChange(t *testing.T) {
	pm := activeCard(11, 2030)
	account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}
	c := fake.NewClientBuilder().WithScheme(eventScheme(t)).
		WithIndex(&billingv1alpha1.PaymentMethod{}, PaymentMethodBillingAccountRefField,
			func(obj client.Object) []string {
				return []string{obj.(*billingv1alpha1.PaymentMethod).Spec.BillingAccountRef.Name}
			}).
		WithObjects(pm, account).Build()
	rec := &captureRecorder{}
	r := &BillingAccountReconciler{client: c, recorder: rec}

	for range 2 {
		if err := r.reconcileDefaultPaymentMethod(context.Background(), account); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.events) != 1 || rec.events[0].reason != billingv1alpha1.EventReasonDefaultPaymentMethodChanged {
		t.Fatalf("recorded %v, want one DefaultPaymentMethodChanged", rec.reasons())
	}
	got := rec.events[0].annotations
	if got[billingv1alpha1.EventAnnotationPaymentMethod] != "visa-4242" || got[billingv1alpha1.EventAnnotationCardLast4] != "4242" {
		t.Errorf("annotations = %v", got)
	}
}
