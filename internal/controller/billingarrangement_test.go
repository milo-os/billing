// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

func day(d int) time.Time {
	return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC)
}

func dayPtr(d int) *metav1.Time {
	t := metav1.NewTime(day(d))
	return &t
}

func testArrangement(name string, startsAt, endsAt *metav1.Time) billingv1alpha1.BillingArrangement {
	return billingv1alpha1.BillingArrangement{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingArrangementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Type:              billingv1alpha1.BillingArrangementTypeInvoice,
			Invoice: &billingv1alpha1.InvoiceArrangementTerms{
				NetDays:             30,
				CreditLimit:         "10000",
				PurchaseOrderNumber: "PO-4471",
			},
			StartsAt: startsAt,
			EndsAt:   endsAt,
			Reason:   "Signed MSA",
		},
	}
}

func TestArrangementPhaseAt(t *testing.T) {
	tests := []struct {
		name         string
		arr          billingv1alpha1.BillingArrangement
		now          time.Time
		want         billingv1alpha1.BillingArrangementPhase
		wantBoundary time.Time
	}{
		{
			name:         "before start is scheduled",
			arr:          testArrangement("a", dayPtr(10), dayPtr(20)),
			now:          day(5),
			want:         billingv1alpha1.BillingArrangementPhaseScheduled,
			wantBoundary: day(10),
		},
		{
			name:         "at start is active",
			arr:          testArrangement("a", dayPtr(10), dayPtr(20)),
			now:          day(10),
			want:         billingv1alpha1.BillingArrangementPhaseActive,
			wantBoundary: day(20),
		},
		{
			name: "at end is ended",
			arr:  testArrangement("a", dayPtr(10), dayPtr(20)),
			now:  day(20),
			want: billingv1alpha1.BillingArrangementPhaseEnded,
		},
		{
			name: "open-ended stays active with no boundary",
			arr:  testArrangement("a", dayPtr(10), nil),
			now:  day(25),
			want: billingv1alpha1.BillingArrangementPhaseActive,
		},
		{
			name: "scheduled terms withdrawn before they start",
			arr:  testArrangement("a", dayPtr(10), dayPtr(5)),
			now:  day(6),
			want: billingv1alpha1.BillingArrangementPhaseEnded,
		},
		{
			name: "unset start falls back to creation time",
			arr: func() billingv1alpha1.BillingArrangement {
				a := testArrangement("a", nil, nil)
				a.CreationTimestamp = metav1.NewTime(day(15))
				return a
			}(),
			now:          day(12),
			want:         billingv1alpha1.BillingArrangementPhaseScheduled,
			wantBoundary: day(15),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validation.ArrangementPhaseAt(&tt.arr, tt.now); got != tt.want {
				t.Errorf("phase = %s, want %s", got, tt.want)
			}
			if got := nextArrangementBoundary(&tt.arr, tt.now); !got.Equal(tt.wantBoundary) {
				t.Errorf("boundary = %s, want %s", got, tt.wantBoundary)
			}
		})
	}
}

func TestPickArrangements(t *testing.T) {
	arrs := []billingv1alpha1.BillingArrangement{
		testArrangement("old", dayPtr(1), dayPtr(5)),
		testArrangement("older", dayPtr(1), dayPtr(3)),
		testArrangement("current", dayPtr(5), dayPtr(20)),
		testArrangement("next", dayPtr(20), nil),
	}

	picked := pickArrangements(arrs, "", day(10))
	if picked.active == nil || picked.active.Name != "current" {
		t.Errorf("active = %v, want current", picked.active)
	}
	if picked.lastEnded == nil || picked.lastEnded.Name != "old" {
		t.Errorf("lastEnded = %v, want old", picked.lastEnded)
	}
	if picked.nextScheduled == nil || picked.nextScheduled.Name != "next" {
		t.Errorf("nextScheduled = %v, want next", picked.nextScheduled)
	}

	picked = pickArrangements(arrs, "", day(25))
	if picked.active == nil || picked.active.Name != "next" {
		t.Errorf("active after handover = %v, want next", picked.active)
	}

	// Overlap slipped past the webhook: the later start wins.
	overlapping := append(arrs, testArrangement("racer", dayPtr(8), nil))
	picked = pickArrangements(overlapping, "", day(10))
	if picked.active == nil || picked.active.Name != "racer" {
		t.Errorf("active with overlap = %v, want racer", picked.active)
	}

	// Terms left over from a deleted account with the same name are
	// ignored until garbage collection removes them.
	orphan := testArrangement("orphan", dayPtr(1), nil)
	orphan.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "deleted-account"}}
	owned := testArrangement("owned", dayPtr(1), nil)
	owned.OwnerReferences = []metav1.OwnerReference{{Kind: "BillingAccount", Name: "acme", UID: "current-account"}}
	picked = pickArrangements([]billingv1alpha1.BillingArrangement{orphan}, "current-account", day(10))
	if picked.active != nil {
		t.Errorf("active = %v, want orphan ignored", picked.active.Name)
	}
	picked = pickArrangements([]billingv1alpha1.BillingArrangement{orphan, owned}, "current-account", day(10))
	if picked.active == nil || picked.active.Name != "owned" {
		t.Errorf("active = %v, want owned", picked.active)
	}
}

func TestReconcilePaymentReadyCondition(t *testing.T) {
	now := day(10)

	tests := []struct {
		name          string
		arrangements  []billingv1alpha1.BillingArrangement
		pmStatus      metav1.ConditionStatus
		pmReason      string
		wantStatus    metav1.ConditionStatus
		wantReason    string
		wantPublished string
		wantNext      time.Time
	}{
		{
			name:       "nothing configured",
			pmStatus:   metav1.ConditionFalse,
			wantStatus: metav1.ConditionFalse,
			wantReason: "NotConfigured",
		},
		{
			name:       "card only",
			pmStatus:   metav1.ConditionTrue,
			wantStatus: metav1.ConditionTrue,
			wantReason: "PaymentMethodReady",
		},
		{
			name:          "active terms without a card",
			arrangements:  []billingv1alpha1.BillingArrangement{testArrangement("terms", dayPtr(1), dayPtr(30))},
			pmStatus:      metav1.ConditionFalse,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "InvoiceTerms",
			wantPublished: "terms",
			wantNext:      day(30),
		},
		{
			name:          "active terms take precedence over a card",
			arrangements:  []billingv1alpha1.BillingArrangement{testArrangement("terms", dayPtr(1), nil)},
			pmStatus:      metav1.ConditionTrue,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "InvoiceTerms",
			wantPublished: "terms",
		},
		{
			name:         "ended terms without a card",
			arrangements: []billingv1alpha1.BillingArrangement{testArrangement("terms", dayPtr(1), dayPtr(5))},
			pmStatus:     metav1.ConditionFalse,
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "ArrangementEnded",
		},
		{
			name:         "ended terms with a card",
			arrangements: []billingv1alpha1.BillingArrangement{testArrangement("terms", dayPtr(1), dayPtr(5))},
			pmStatus:     metav1.ConditionTrue,
			wantStatus:   metav1.ConditionTrue,
			wantReason:   "PaymentMethodReady",
		},
		{
			name:         "scheduled terms requeue at their start",
			arrangements: []billingv1alpha1.BillingArrangement{testArrangement("terms", dayPtr(15), nil)},
			pmStatus:     metav1.ConditionFalse,
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "ArrangementScheduled",
			wantNext:     day(15),
		},
		{
			name: "renewal scheduled after terms ended",
			arrangements: []billingv1alpha1.BillingArrangement{
				testArrangement("old", dayPtr(1), dayPtr(5)),
				testArrangement("renewal", dayPtr(12), nil),
			},
			pmStatus:   metav1.ConditionFalse,
			wantStatus: metav1.ConditionFalse,
			wantReason: "ArrangementScheduled",
			wantNext:   day(12),
		},
		{
			name:       "failed card is reported, not NotConfigured",
			pmStatus:   metav1.ConditionFalse,
			pmReason:   "PaymentMethodDegraded",
			wantStatus: metav1.ConditionFalse,
			wantReason: "PaymentMethodDegraded",
		},
		{
			name:       "unreadable card is unknown",
			pmStatus:   metav1.ConditionUnknown,
			wantStatus: metav1.ConditionUnknown,
			wantReason: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := billingv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			objs := make([]client.Object, 0, len(tt.arrangements))
			for i := range tt.arrangements {
				objs = append(objs, &tt.arrangements[i])
			}
			// Arrangements for another account must be ignored.
			other := testArrangement("globex-terms", dayPtr(1), nil)
			other.Spec.BillingAccountRef.Name = "globex"
			objs = append(objs, &other)

			c := fake.NewClientBuilder().WithScheme(scheme).
				WithIndex(&billingv1alpha1.BillingArrangement{}, ArrangementBillingAccountRefField,
					func(obj client.Object) []string {
						return []string{obj.(*billingv1alpha1.BillingArrangement).Spec.BillingAccountRef.Name}
					}).
				WithObjects(objs...).Build()

			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
			}
			pmReason := tt.pmReason
			if pmReason == "" {
				pmReason = "NotConfigured"
				if tt.pmStatus == metav1.ConditionTrue {
					pmReason = "Ready"
				}
			}
			apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
				Type:   billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady,
				Status: tt.pmStatus,
				Reason: pmReason,
			})

			r := &BillingAccountReconciler{client: c}
			next, err := r.reconcilePaymentReadyCondition(context.Background(), account, now)
			if err != nil {
				t.Fatal(err)
			}

			cond := apimeta.FindStatusCondition(account.Status.Conditions, billingv1alpha1.BillingAccountConditionPaymentReady)
			if cond == nil {
				t.Fatal("PaymentReady condition not set")
			}
			if cond.Status != tt.wantStatus || cond.Reason != tt.wantReason {
				t.Errorf("condition = %s/%s, want %s/%s", cond.Status, cond.Reason, tt.wantStatus, tt.wantReason)
			}

			published := ""
			if account.Status.PaymentArrangement != nil {
				published = account.Status.PaymentArrangement.Name
				if account.Status.PaymentArrangement.Invoice.PurchaseOrderNumber != "PO-4471" {
					t.Errorf("published terms missing PO number: %+v", account.Status.PaymentArrangement.Invoice)
				}
			}
			if published != tt.wantPublished {
				t.Errorf("paymentArrangement = %q, want %q", published, tt.wantPublished)
			}
			if !next.Equal(tt.wantNext) {
				t.Errorf("next boundary = %s, want %s", next, tt.wantNext)
			}
		})
	}
}
