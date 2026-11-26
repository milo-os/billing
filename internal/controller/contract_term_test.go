// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func utcDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// termAccountClient serves an account entitled to a contract Offer with a
// monthly fee, and a pay-as-you-go Offer to stop to.
func termAccountClient(t *testing.T, be *billingv1alpha1.BillingEntitlement) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(setupTestScheme(t)).
		WithObjects(
			be,
			publishedOffer("acme-contract", billingv1alpha1.OfferLaunchStageGA,
				fee("acme-monthly", "Monthly fee", billingv1alpha1.ChargeTypeRecurring, "2400.00"),
				snapshot("alb-requests", "ALB Requests", -1)),
			publishedOffer("payg", billingv1alpha1.OfferLaunchStageGA, snapshot("alb-requests", "ALB Requests", -1)),
			publishedOffer("withdrawn", billingv1alpha1.OfferLaunchStageDraft, snapshot("alb-requests", "ALB Requests", -1)),
			&billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"}},
		).
		WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingEntitlement).Spec.BillingAccountRef.Name}
		}).
		WithStatusSubresource(&billingv1alpha1.BillingEntitlement{}).
		Build()
}

func termEntitlement(offer string, term *billingv1alpha1.ContractTerm) *billingv1alpha1.BillingEntitlement {
	be := contractEntitlement(offer, utcDate(2026, time.April, 18))
	be.Spec.Contract.Term = term
	return be
}

func stopTerm(stopsAt *metav1.Time) *billingv1alpha1.ContractTerm {
	return stopTermTo("payg", stopsAt)
}

func stopTermTo(offer string, stopsAt *metav1.Time) *billingv1alpha1.ContractTerm {
	return &billingv1alpha1.ContractTerm{
		Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop,
		OfferAfterRef: &billingv1alpha1.OfferReference{Name: offer}, StopsAt: stopsAt,
	}
}

func TestReconcileContractTerm(t *testing.T) {
	tests := []struct {
		name       string
		term       *billingv1alpha1.ContractTerm
		moved      bool
		now        time.Time
		wantPhase  billingv1alpha1.ContractTermPhase
		wantEndsAt time.Time
		wantReason string
		wantStatus metav1.ConditionStatus
		wantFees   bool
		wantNext   time.Time
	}{
		{
			name: "in its term", term: &billingv1alpha1.ContractTerm{Months: 36}, now: utcDate(2027, time.June, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseInTerm, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonInTerm, wantStatus: metav1.ConditionFalse,
			wantFees: true, wantNext: utcDate(2029, time.January, 18),
		},
		{
			name: "renewal due", term: &billingv1alpha1.ContractTerm{Months: 36}, now: utcDate(2029, time.February, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseInTerm, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonTermEndingSoon, wantStatus: metav1.ConditionTrue,
			wantFees: true, wantNext: utcDate(2029, time.April, 18),
		},
		{
			name: "month to month", term: &billingv1alpha1.ContractTerm{Months: 36}, now: utcDate(2029, time.May, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseMonthToMonth, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonMonthToMonth, wantStatus: metav1.ConditionTrue,
			wantFees: true,
		},
		{
			name: "stopped", term: stopTerm(nil), moved: true, now: utcDate(2029, time.May, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseStopped, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopped, wantStatus: metav1.ConditionFalse,
		},
		{
			name: "set to stop onto an Offer that's usable", term: stopTerm(nil), now: utcDate(2029, time.February, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseInTerm, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonTermEndingSoon, wantStatus: metav1.ConditionTrue,
			wantFees: true, wantNext: utcDate(2029, time.February, 1).Add(contractStopRecheck),
		},
		{
			name: "set to stop onto an Offer that was withdrawn", term: stopTermTo("withdrawn", nil), now: utcDate(2029, time.February, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseInTerm, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopBlocked, wantStatus: metav1.ConditionTrue,
			wantFees: true, wantNext: utcDate(2029, time.February, 1).Add(contractStopRecheck),
		},
		{
			name: "set to stop, but not near it yet", term: stopTermTo("withdrawn", nil), now: utcDate(2027, time.June, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseInTerm, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonInTerm, wantStatus: metav1.ConditionFalse,
			wantFees: true, wantNext: utcDate(2029, time.January, 18),
		},
		{
			name: "just stopped, waiting for billing to move it", term: stopTerm(nil), now: utcDate(2029, time.April, 18).Add(time.Minute),
			wantPhase: billingv1alpha1.ContractTermPhaseStopped, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopped, wantStatus: metav1.ConditionFalse,
			wantFees: true, wantNext: utcDate(2029, time.April, 18).Add(time.Minute + contractStopRecheck),
		},
		{
			name: "stopped, but never moved", term: stopTermTo("withdrawn", nil), now: utcDate(2029, time.May, 1),
			wantPhase: billingv1alpha1.ContractTermPhaseStopped, wantEndsAt: utcDate(2029, time.April, 18),
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopBlocked, wantStatus: metav1.ConditionTrue,
			wantFees: true, wantNext: utcDate(2029, time.May, 1).Add(contractStopRecheck),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := termEntitlement("acme-contract", tt.term)
			if tt.moved {
				be.Annotations = map[string]string{billingv1alpha1.BillingEntitlementContractStoppedAtAnnotation: "2029-04-18T00:00:00Z"}
			}
			cl := termAccountClient(t, be)
			account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"}}
			r := &BillingAccountReconciler{client: cl}
			next, err := r.reconcileEntitlementSummary(context.Background(), account, tt.now)
			if err != nil {
				t.Fatalf("reconcileEntitlementSummary: %v", err)
			}
			term := account.Status.Contract.Term
			if term == nil {
				t.Fatal("status.contract.term is empty")
			}
			if term.Phase != tt.wantPhase || !term.EndsAt.Time.Equal(tt.wantEndsAt) {
				t.Errorf("term = %s until %s, want %s until %s", term.Phase, term.EndsAt.Time, tt.wantPhase, tt.wantEndsAt)
			}
			if gotFees := len(account.Status.Contract.Fees) > 0; gotFees != tt.wantFees {
				t.Errorf("fees published = %v, want %v", gotFees, tt.wantFees)
			}
			cond := apimeta.FindStatusCondition(account.Status.Conditions, billingv1alpha1.BillingAccountConditionContractRenewalDue)
			if cond == nil || cond.Reason != tt.wantReason || cond.Status != tt.wantStatus {
				t.Errorf("ContractRenewalDue = %+v, want %s %s", cond, tt.wantStatus, tt.wantReason)
			}
			if !next.Equal(tt.wantNext) {
				t.Errorf("next boundary = %s, want %s", next, tt.wantNext)
			}
		})
	}

	t.Run("no contract", func(t *testing.T) {
		cl := termAccountClient(t, entitlement("acme", "payg"))
		account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"}}
		r := &BillingAccountReconciler{client: cl}
		if _, err := r.reconcileEntitlementSummary(context.Background(), account, utcDate(2027, time.June, 1)); err != nil {
			t.Fatal(err)
		}
		cond := apimeta.FindStatusCondition(account.Status.Conditions, billingv1alpha1.BillingAccountConditionContractRenewalDue)
		if cond == nil || cond.Reason != billingv1alpha1.ContractRenewalDueReasonNoTerm || cond.Status != metav1.ConditionFalse {
			t.Errorf("ContractRenewalDue = %+v, want False NoTerm", cond)
		}
	})
}

func TestRecordContractTermTransition(t *testing.T) {
	term := func(phase billingv1alpha1.ContractTermPhase, atEnd billingv1alpha1.ContractTermAtEnd, start, end time.Time, renewals int32, stopsAt *time.Time) *billingv1alpha1.BillingAccountContractTerm {
		out := &billingv1alpha1.BillingAccountContractTerm{
			Months: 36, AtEnd: atEnd, Phase: phase, Renewals: renewals,
			CurrentTermStartsAt: metav1.NewTime(start), EndsAt: metav1.NewTime(end),
		}
		if atEnd == billingv1alpha1.ContractTermAtEndRenew {
			out.RenewMonths = 12
		}
		if stopsAt != nil {
			s := metav1.NewTime(*stopsAt)
			out.StopsAt = &s
		}
		return out
	}
	cond := func(reason string) *metav1.Condition {
		return &metav1.Condition{Type: billingv1alpha1.BillingAccountConditionContractRenewalDue, Reason: reason, Message: "msg"}
	}
	start, end, renewedEnd := utcDate(2026, time.April, 18), utcDate(2029, time.April, 18), utcDate(2030, time.April, 18)
	inTerm, m2m, stopped := billingv1alpha1.ContractTermPhaseInTerm, billingv1alpha1.ContractTermPhaseMonthToMonth, billingv1alpha1.ContractTermPhaseStopped
	monthly, renew, stop := billingv1alpha1.ContractTermAtEndMonthToMonth, billingv1alpha1.ContractTermAtEndRenew, billingv1alpha1.ContractTermAtEndStop
	reasonInTerm, soon := billingv1alpha1.ContractRenewalDueReasonInTerm, billingv1alpha1.ContractRenewalDueReasonTermEndingSoon

	tests := []struct {
		name                  string
		before, after         *billingv1alpha1.BillingAccountContractTerm
		condBefore, condAfter *metav1.Condition
		want                  []string
		wantScoped            bool
	}{
		{
			name: "renewal due", before: term(inTerm, monthly, start, end, 0, nil), after: term(inTerm, monthly, start, end, 0, nil),
			condBefore: cond(reasonInTerm), condAfter: cond(soon), want: []string{billingv1alpha1.EventReasonContractRenewalDue},
		},
		{
			name: "staff adding a term inside the notice records nothing", before: nil, after: term(inTerm, monthly, start, end, 0, nil),
			condBefore: cond(billingv1alpha1.ContractRenewalDueReasonNoTerm), condAfter: cond(soon),
		},
		{
			name: "term ended", before: term(inTerm, monthly, start, end, 0, nil), after: term(m2m, monthly, start, end, 0, nil),
			condBefore: cond(soon), condAfter: cond(billingv1alpha1.ContractRenewalDueReasonMonthToMonth),
			want: []string{billingv1alpha1.EventReasonContractTermEnded}, wantScoped: true,
		},
		{
			name: "renewed", before: term(inTerm, renew, start, end, 0, nil), after: term(inTerm, renew, end, renewedEnd, 1, nil),
			condBefore: cond(soon), condAfter: cond(reasonInTerm),
			want: []string{billingv1alpha1.EventReasonContractRenewed}, wantScoped: true,
		},
		{
			name: "stopped", before: term(inTerm, stop, start, end, 0, &end), after: term(stopped, stop, start, end, 0, &end),
			condBefore: cond(soon), condAfter: cond(billingv1alpha1.ContractRenewalDueReasonStopped),
			want: []string{billingv1alpha1.EventReasonContractStopped}, wantScoped: true,
		},
		{
			name: "staff lengthening the term records nothing", before: term(inTerm, monthly, start, end, 0, nil),
			after: term(inTerm, monthly, start, renewedEnd, 0, nil), condBefore: cond(soon), condAfter: cond(reasonInTerm),
		},
		{
			name: "nothing changed", before: term(inTerm, monthly, start, end, 0, nil), after: term(inTerm, monthly, start, end, 0, nil),
			condBefore: cond(soon), condAfter: cond(soon),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &captureRecorder{}
			account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"}}
			recordContractTermTransition(rec, account, tt.before, tt.after, tt.condBefore, tt.condAfter)
			if got := rec.reasons(); !slices.Equal(got, tt.want) {
				t.Fatalf("events = %v, want %v", got, tt.want)
			}
			for _, e := range rec.events {
				_, scoped := e.annotations[billingv1alpha1.ScopeTypeAnnotation]
				if scoped != tt.wantScoped {
					t.Errorf("%s organization scoped = %v, want %v", e.reason, scoped, tt.wantScoped)
				}
			}
		})
	}
}

func TestBillingEntitlementReconcileStopsContract(t *testing.T) {
	be := termEntitlement("acme-contract", stopTerm(nil))
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "org-acme", Name: "acme"}}
	get := func(cl client.Client) *billingv1alpha1.BillingEntitlement {
		var got billingv1alpha1.BillingEntitlement
		if err := cl.Get(ctx, req.NamespacedName, &got); err != nil {
			t.Fatal(err)
		}
		return &got
	}

	t.Run("before the stop it waits for it", func(t *testing.T) {
		cl := termAccountClient(t, be.DeepCopy())
		now := utcDate(2029, time.April, 17)
		r := &BillingEntitlementReconciler{client: cl, now: func() time.Time { return now }}
		res, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if got := get(cl).Spec.OfferRef.Name; got != "acme-contract" {
			t.Errorf("offerRef = %s before the stop", got)
		}
		if res.RequeueAfter != 24*time.Hour+time.Second {
			t.Errorf("RequeueAfter = %s, want a day and a second", res.RequeueAfter)
		}
	})

	t.Run("at the stop it moves the account onto offerAfterRef, once", func(t *testing.T) {
		cl := termAccountClient(t, be.DeepCopy())
		now := utcDate(2029, time.April, 18)
		r := &BillingEntitlementReconciler{client: cl, now: func() time.Time { return now }}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		got := get(cl)
		if got.Spec.OfferRef.Name != "payg" {
			t.Fatalf("offerRef = %s, want payg", got.Spec.OfferRef.Name)
		}
		if got.Annotations[billingv1alpha1.BillingEntitlementContractStoppedAtAnnotation] != "2029-04-18T00:00:00Z" {
			t.Errorf("annotations = %v", got.Annotations)
		}

		// Staff move the account onto another Offer after the stop.
		got.Spec.OfferRef.Name = "acme-contract"
		if err := cl.Update(ctx, got); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if name := get(cl).Spec.OfferRef.Name; name != "acme-contract" {
			t.Errorf("offerRef = %s after staff changed it, want it left alone", name)
		}
	})
}

func TestBillingEntitlementReconcileStopRefused(t *testing.T) {
	ctx := context.Background()
	be := termEntitlement("acme-contract", stopTerm(nil))
	scheme := setupTestScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(be, &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"}}).
		WithStatusSubresource(&billingv1alpha1.BillingEntitlement{}).
		WithInterceptorFuncs(interceptor.Funcs{
			// The webhook refusing the Offer, retired since the term was set.
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return apierrors.NewInvalid(schema.GroupKind{Group: "billing.miloapis.com", Kind: "BillingEntitlement"}, obj.GetName(),
					field.ErrorList{field.Invalid(field.NewPath("spec", "offerRef", "name"), "payg", "offer \"payg\" isn't published")})
			},
		}).
		Build()
	rec := &captureRecorder{}
	now := utcDate(2029, time.April, 18)
	r := &BillingEntitlementReconciler{client: cl, recorder: rec, now: func() time.Time { return now }}

	res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "org-acme", Name: "acme"}})
	if err != nil {
		t.Fatalf("Reconcile() = %v, want the refusal reported rather than retried at once", err)
	}
	if res.RequeueAfter != contractStopRecheck {
		t.Errorf("RequeueAfter = %s, want %s", res.RequeueAfter, contractStopRecheck)
	}
	if got := rec.reasons(); !slices.Equal(got, []string{billingv1alpha1.EventReasonContractStopBlocked}) {
		t.Fatalf("events = %v", got)
	}
	e := rec.events[0]
	if _, isAccount := e.object.(*billingv1alpha1.BillingAccount); !isAccount || e.eventType != "Warning" {
		t.Errorf("event on %T of type %s, want a warning on the BillingAccount", e.object, e.eventType)
	}
	if _, scoped := e.annotations[billingv1alpha1.ScopeTypeAnnotation]; scoped || e.annotations[billingv1alpha1.EventAnnotationOffer] != "payg" {
		t.Errorf("annotations = %v, want the offer and no organization scope", e.annotations)
	}
}
