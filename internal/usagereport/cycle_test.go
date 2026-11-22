// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestBillingCyclesDefaultToCalendarMonths(t *testing.T) {
	now := time.Date(2026, time.October, 21, 9, 30, 0, 0, time.UTC)
	current, previous := BillingCycles(nil, now)

	if !current.Start.Equal(date(2026, time.October, 1)) || !current.PeriodEnd.Equal(date(2026, time.November, 1)) {
		t.Errorf("current = %v - %v", current.Start, current.PeriodEnd)
	}
	if !current.End.Equal(now) {
		t.Errorf("current cycle should end now, got %v", current.End)
	}
	if !previous.Start.Equal(date(2026, time.September, 1)) || !previous.End.Equal(date(2026, time.October, 1)) {
		t.Errorf("previous = %v - %v", previous.Start, previous.End)
	}
}

func TestBillingCyclesFollowInvoiceDay(t *testing.T) {
	terms := &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 15}

	current, _ := BillingCycles(terms, date(2026, time.October, 10))
	if !current.Start.Equal(date(2026, time.September, 15)) || !current.PeriodEnd.Equal(date(2026, time.October, 15)) {
		t.Errorf("before the invoice day: %v - %v", current.Start, current.PeriodEnd)
	}

	current, _ = BillingCycles(terms, date(2026, time.October, 20))
	if !current.Start.Equal(date(2026, time.October, 15)) {
		t.Errorf("after the invoice day: %v", current.Start)
	}
}

func TestBillingCyclesQuarterly(t *testing.T) {
	terms := &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Quarterly", InvoiceDayOfMonth: 1}
	current, previous := BillingCycles(terms, date(2026, time.October, 21))
	if !current.Start.Equal(date(2026, time.October, 1)) || !current.PeriodEnd.Equal(date(2027, time.January, 1)) {
		t.Errorf("current = %v - %v", current.Start, current.PeriodEnd)
	}
	if !previous.Start.Equal(date(2026, time.July, 1)) {
		t.Errorf("previous starts %v", previous.Start)
	}
}

func TestCycleTermsFollowAStartedContract(t *testing.T) {
	account := &billingv1alpha1.BillingAccount{
		Spec: billingv1alpha1.BillingAccountSpec{
			PaymentTerms: &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 1, NetDays: 30},
		},
		Status: billingv1alpha1.BillingAccountStatus{
			Contract: &billingv1alpha1.BillingAccountContract{
				StartsAt:          metav1.NewTime(date(2026, time.March, 31)),
				BillingDayOfMonth: 31,
			},
		},
	}

	// Before the contract starts, the payment terms decide.
	if got := CycleTerms(account, date(2026, time.March, 30)); got.InvoiceDayOfMonth != 1 {
		t.Errorf("before the start, day = %d, want 1", got.InvoiceDayOfMonth)
	}

	// Once it has, the contract's day does, clamped in shorter months.
	terms := CycleTerms(account, date(2026, time.April, 10))
	if terms.InvoiceDayOfMonth != 31 || terms.NetDays != 30 {
		t.Errorf("terms = %+v, want the contract's day and the account's net days", terms)
	}
	if account.Spec.PaymentTerms.InvoiceDayOfMonth != 1 {
		t.Error("CycleTerms changed the account's own terms")
	}
	current, previous := BillingCycles(terms, date(2026, time.April, 10))
	if !current.Start.Equal(date(2026, time.March, 31)) || !current.PeriodEnd.Equal(date(2026, time.April, 30)) {
		t.Errorf("current = %v - %v, want 31 Mar - 30 Apr", current.Start, current.PeriodEnd)
	}
	if !previous.Start.Equal(date(2026, time.February, 28)) {
		t.Errorf("previous starts %v, want 28 Feb", previous.Start)
	}

	// A contract account without payment terms still bills monthly on its day.
	account.Spec.PaymentTerms = nil
	if got := CycleTerms(account, date(2026, time.April, 10)); got.InvoiceFrequency != "Monthly" || got.InvoiceDayOfMonth != 31 {
		t.Errorf("without payment terms = %+v", got)
	}
	if got := CycleTerms(nil, date(2026, time.April, 10)); got != nil {
		t.Errorf("no account = %+v, want nil", got)
	}
}

func TestRangeLabel(t *testing.T) {
	if got := RangeLabel(date(2026, time.September, 30), date(2026, time.October, 30)); got != "30 Sep - 30 Oct 2026" {
		t.Errorf("got %q", got)
	}
	if got := RangeLabel(date(2026, time.December, 15), date(2027, time.January, 15)); got != "15 Dec 2026 - 15 Jan 2027" {
		t.Errorf("got %q", got)
	}
}
