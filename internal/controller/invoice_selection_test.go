// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestPickLatestInvoice(t *testing.T) {
	earlier := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	later := metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name     string
		invoices []billingv1alpha1.Invoice
		want     string
	}{
		{
			name: "newer creationTimestamp wins",
			invoices: []billingv1alpha1.Invoice{
				{ObjectMeta: metav1.ObjectMeta{Name: "a", CreationTimestamp: earlier}},
				{ObjectMeta: metav1.ObjectMeta{Name: "b", CreationTimestamp: later}},
			},
			want: "b",
		},
		{
			name: "equal timestamps pick lexicographically greater name",
			invoices: []billingv1alpha1.Invoice{
				{ObjectMeta: metav1.ObjectMeta{Name: "acct-2026-01", CreationTimestamp: earlier}},
				{ObjectMeta: metav1.ObjectMeta{Name: "acct-2026-02", CreationTimestamp: earlier}},
			},
			want: "acct-2026-02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickLatestInvoice(tt.invoices)
			if got.Name != tt.want {
				t.Fatalf("pickLatestInvoice() = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestPickReadinessInvoice(t *testing.T) {
	earlier := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	later := metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name     string
		invoices []billingv1alpha1.Invoice
		wantName string
		wantNil  bool
	}{
		{
			name: "skips void in favor of older past due",
			invoices: []billingv1alpha1.Invoice{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "past-due", CreationTimestamp: earlier},
					Status:     billingv1alpha1.InvoiceStatus{Phase: billingv1alpha1.InvoicePhasePastDue},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "voided", CreationTimestamp: later},
					Status:     billingv1alpha1.InvoiceStatus{Phase: billingv1alpha1.InvoicePhaseVoid},
				},
			},
			wantName: "past-due",
		},
		{
			name: "skips empty phase",
			invoices: []billingv1alpha1.Invoice{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pending", CreationTimestamp: later},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "paid", CreationTimestamp: earlier},
					Status:     billingv1alpha1.InvoiceStatus{Phase: billingv1alpha1.InvoicePhasePaid},
				},
			},
			wantName: "paid",
		},
		{
			name: "nil when only void and empty",
			invoices: []billingv1alpha1.Invoice{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "voided", CreationTimestamp: earlier},
					Status:     billingv1alpha1.InvoiceStatus{Phase: billingv1alpha1.InvoicePhaseVoid},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pending", CreationTimestamp: later},
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickReadinessInvoice(tt.invoices)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %q", got.Name)
				}
				return
			}
			if got == nil || got.Name != tt.wantName {
				t.Fatalf("pickReadinessInvoice() = %v, want %q", got, tt.wantName)
			}
		})
	}
}
