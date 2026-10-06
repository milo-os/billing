// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func arrangementTime(day int) *metav1.Time {
	t := metav1.NewTime(time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC))
	return &t
}

func invoiceArrangement(name string, startsAt, endsAt *metav1.Time) *billingv1alpha1.BillingArrangement {
	return &billingv1alpha1.BillingArrangement{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme", UID: types.UID(name)},
		Spec: billingv1alpha1.BillingArrangementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Type:              billingv1alpha1.BillingArrangementTypeInvoice,
			Invoice: &billingv1alpha1.InvoiceArrangementTerms{
				NetDays:     30,
				CreditLimit: "10000",
			},
			StartsAt: startsAt,
			EndsAt:   endsAt,
			Reason:   "Signed MSA",
		},
	}
}

func arrangementTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, account)...).Build()
}

func TestValidateBillingArrangementCreate(t *testing.T) {
	now := arrangementTime(6).Time

	tests := []struct {
		name     string
		existing []client.Object
		mutate   func(*billingv1alpha1.BillingArrangement)
		wantErr  bool
	}{
		{
			name: "valid open-ended invoice terms",
		},
		{
			name: "valid with every optional invoice field",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(30)
				a.Spec.Invoice.AccountsPayableEmail = "ap@acme.example"
				a.Spec.Invoice.PurchaseOrderNumber = "PO-4471"
				a.Spec.Invoice.AgreementReference = "MSA-2026-014"
			},
		},
		{
			name: "missing billing account",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.BillingAccountRef.Name = "nope"
			},
			wantErr: true,
		},
		{
			name: "endsAt not after startsAt",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(6)
			},
			wantErr: true,
		},
		{
			name: "endsAt checked against defaulted start when startsAt is unset",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.StartsAt = nil
				a.Spec.EndsAt = arrangementTime(5)
			},
			wantErr: true,
		},
		{
			name: "zero credit limit",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.Invoice.CreditLimit = "0"
			},
			wantErr: true,
		},
		{
			name: "display-name style AP email",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.Invoice.AccountsPayableEmail = "Accounts <ap@acme.example>"
			},
			wantErr: true,
		},
		{
			name:     "overlaps an open-ended arrangement",
			existing: []client.Object{invoiceArrangement("earlier", arrangementTime(1), nil)},
			wantErr:  true,
		},
		{
			name:     "overlaps a bounded arrangement",
			existing: []client.Object{invoiceArrangement("earlier", arrangementTime(1), arrangementTime(10))},
			wantErr:  true,
		},
		{
			name:     "starts exactly when the previous arrangement ends",
			existing: []client.Object{invoiceArrangement("earlier", arrangementTime(1), arrangementTime(6))},
		},
		{
			name:     "ends exactly when a later arrangement starts",
			existing: []client.Object{invoiceArrangement("later", arrangementTime(20), nil)},
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(20)
			},
		},
		{
			name: "arrangement for another account does not overlap",
			existing: []client.Object{func() client.Object {
				other := invoiceArrangement("other", arrangementTime(1), nil)
				other.Spec.BillingAccountRef.Name = "globex"
				return other
			}()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arr := invoiceArrangement("new", arrangementTime(6), nil)
			if tt.mutate != nil {
				tt.mutate(arr)
			}
			c := arrangementTestClient(t, tt.existing...)
			errs := ValidateBillingArrangementCreate(context.Background(), c, arr, now)
			if gotErr := len(errs) > 0; gotErr != tt.wantErr {
				t.Errorf("wantErr=%v, got %v", tt.wantErr, errs)
			}
		})
	}
}

func TestValidateBillingArrangementUpdate(t *testing.T) {
	now := arrangementTime(6).Time

	tests := []struct {
		name     string
		existing []client.Object
		mutate   func(*billingv1alpha1.BillingArrangement)
		wantErr  bool
	}{
		{
			name: "ending terms early",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(7)
			},
		},
		{
			name: "amending the credit limit",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.Invoice.CreditLimit = "25000.50"
			},
		},
		{
			name: "changing billing account",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.BillingAccountRef.Name = "globex"
			},
			wantErr: true,
		},
		{
			name:     "extending into a later arrangement",
			existing: []client.Object{invoiceArrangement("later", arrangementTime(20), nil)},
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(25)
			},
			wantErr: true,
		},
		{
			name: "withdrawing terms with an endsAt in the past before startsAt",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.StartsAt = arrangementTime(5)
				a.Spec.EndsAt = arrangementTime(4)
			},
		},
		{
			name: "future endsAt before startsAt is rejected",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(8)
				a.Spec.StartsAt = arrangementTime(10)
			},
			wantErr: true,
		},
		{
			name:     "ending terms that already overlap another arrangement",
			existing: []client.Object{invoiceArrangement("overlapping", arrangementTime(3), nil)},
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.EndsAt = arrangementTime(6)
			},
		},
		{
			name:     "editing the reason on terms that already overlap",
			existing: []client.Object{invoiceArrangement("overlapping", arrangementTime(3), nil)},
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.Reason = "Clarified after audit"
			},
		},
		{
			name: "does not overlap itself",
			mutate: func(a *billingv1alpha1.BillingArrangement) {
				a.Spec.Reason = "Renewed after review"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArr := invoiceArrangement("current", arrangementTime(1), arrangementTime(15))
			newArr := oldArr.DeepCopy()
			if tt.mutate != nil {
				tt.mutate(newArr)
			}
			c := arrangementTestClient(t, append(tt.existing, oldArr)...)
			errs := ValidateBillingArrangementUpdate(context.Background(), c, oldArr, newArr, now)
			if gotErr := len(errs) > 0; gotErr != tt.wantErr {
				t.Errorf("wantErr=%v, got %v", tt.wantErr, errs)
			}
		})
	}
}
