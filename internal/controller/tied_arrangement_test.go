// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// tiedTermsClient serves acme's account, its entitlement with a 36-month
// contract from 18 April 2026 (term), and invoice terms from the start
// that end with the contract.
func tiedTermsClient(t *testing.T, term *billingv1alpha1.ContractTerm) (client.Client, *billingv1alpha1.BillingArrangement) {
	t.Helper()
	be := termEntitlement("acme-contract", term)
	be.Namespace = "organization-acme"
	arr := &billingv1alpha1.BillingArrangement{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acme-terms", Namespace: "organization-acme", Generation: 1,
			CreationTimestamp: metav1.NewTime(utcDate(2026, time.April, 1)),
		},
		Spec: billingv1alpha1.BillingArrangementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Type:              billingv1alpha1.BillingArrangementTypeInvoice,
			Invoice:           &billingv1alpha1.InvoiceArrangementTerms{NetDays: 30},
			EndsWithContract:  true,
		},
	}
	arr.Status.Phase = billingv1alpha1.BillingArrangementPhaseActive
	arr.Status.ObservedGeneration = 1
	cl := fake.NewClientBuilder().WithScheme(eventScheme(t)).
		WithObjects(be, arr, &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}).
		WithStatusSubresource(arr).
		WithIndex(&billingv1alpha1.BillingArrangement{}, ArrangementBillingAccountRefField, func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingArrangement).Spec.BillingAccountRef.Name}
		}).
		WithIndex(&billingv1alpha1.BillingArrangementApplication{}, ApplicationBillingAccountRefField, func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingArrangementApplication).Spec.BillingAccountRef.Name}
		}).
		Build()
	return cl, arr
}

func TestTiedTermsEndWithTheContract(t *testing.T) {
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "organization-acme", Name: "acme-terms"}}
	stop := utcDate(2029, time.April, 18)

	t.Run("they carry on while the contract does", func(t *testing.T) {
		cl, _ := tiedTermsClient(t, &billingv1alpha1.ContractTerm{Months: 36})
		r := &BillingArrangementReconciler{client: cl, recorder: &captureRecorder{}, now: func() time.Time { return stop.AddDate(1, 0, 0) }}
		res, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		var got billingv1alpha1.BillingArrangement
		if err := cl.Get(ctx, req.NamespacedName, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status.Phase != billingv1alpha1.BillingArrangementPhaseActive || res.RequeueAfter != 0 {
			t.Errorf("phase = %s, requeue %s, want Active with nothing to wait for", got.Status.Phase, res.RequeueAfter)
		}
	})

	t.Run("they end when the contract stops", func(t *testing.T) {
		cl, _ := tiedTermsClient(t, stopTerm(nil))
		rec := &captureRecorder{}
		now := stop.Add(-time.Hour)
		r := &BillingArrangementReconciler{client: cl, recorder: rec, now: func() time.Time { return now }}
		res, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.RequeueAfter != time.Hour+time.Second {
			t.Errorf("RequeueAfter = %s, want the stop", res.RequeueAfter)
		}
		now = stop.Add(time.Second)
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		var got billingv1alpha1.BillingArrangement
		if err := cl.Get(ctx, req.NamespacedName, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status.Phase != billingv1alpha1.BillingArrangementPhaseEnded {
			t.Errorf("phase = %s, want Ended", got.Status.Phase)
		}
		if len(rec.events) != 1 || rec.events[0].reason != billingv1alpha1.EventReasonPaymentTermsEnded ||
			rec.events[0].annotations[billingv1alpha1.EventAnnotationEndsAt] != "2029-04-18T00:00:00Z" {
			t.Errorf("events = %+v, want PaymentTermsEnded at the stop", rec.events)
		}
	})

	t.Run("the account publishes the contract's stop as their end", func(t *testing.T) {
		cl, _ := tiedTermsClient(t, stopTerm(nil))
		account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}
		r := &BillingAccountReconciler{client: cl}
		next, err := r.reconcilePaymentReadyCondition(ctx, account, utcDate(2028, time.June, 1))
		if err != nil {
			t.Fatal(err)
		}
		pa := account.Status.PaymentArrangement
		if pa == nil || !pa.EndsWithContract || pa.EndsAt == nil || !pa.EndsAt.Time.Equal(stop) {
			t.Fatalf("paymentArrangement = %+v, want tied terms ending on %s", pa, stop)
		}
		if !next.Equal(stop) {
			t.Errorf("next boundary = %s, want the stop", next)
		}
	})

	t.Run("the account publishes no end while the contract carries on", func(t *testing.T) {
		cl, _ := tiedTermsClient(t, &billingv1alpha1.ContractTerm{Months: 36})
		account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}
		r := &BillingAccountReconciler{client: cl}
		if _, err := r.reconcilePaymentReadyCondition(ctx, account, utcDate(2028, time.June, 1)); err != nil {
			t.Fatal(err)
		}
		if pa := account.Status.PaymentArrangement; pa == nil || !pa.EndsWithContract || pa.EndsAt != nil {
			t.Errorf("paymentArrangement = %+v, want tied terms with no end", pa)
		}
	})
}

func TestTiedArrangementsAreRequeuedForTheirAccount(t *testing.T) {
	cl, arr := tiedTermsClient(t, &billingv1alpha1.ContractTerm{Months: 36})
	untied := arr.DeepCopy()
	untied.Name, untied.ResourceVersion = "acme-old-terms", ""
	untied.Spec.EndsWithContract = false
	if err := cl.Create(context.Background(), untied); err != nil {
		t.Fatal(err)
	}
	r := &BillingArrangementReconciler{client: cl}
	reqs := r.tiedArrangements(context.Background(), "organization-acme", "acme")
	if len(reqs) != 1 || reqs[0].Name != "acme-terms" {
		t.Errorf("requeued %v, want only the tied terms", reqs)
	}
}
