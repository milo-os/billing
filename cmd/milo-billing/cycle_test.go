package main

import (
	"testing"
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestBillingCyclesDefaultToCalendarMonths(t *testing.T) {
	now := time.Date(2026, time.October, 21, 9, 30, 0, 0, time.UTC)
	current, previous := billingCycles(nil, now)

	if !current.start.Equal(date(2026, time.October, 1)) || !current.periodEnd.Equal(date(2026, time.November, 1)) {
		t.Errorf("current = %v - %v", current.start, current.periodEnd)
	}
	if !current.end.Equal(now) {
		t.Errorf("current cycle should end now, got %v", current.end)
	}
	if !previous.start.Equal(date(2026, time.September, 1)) || !previous.end.Equal(date(2026, time.October, 1)) {
		t.Errorf("previous = %v - %v", previous.start, previous.end)
	}
}

func TestBillingCyclesFollowInvoiceDay(t *testing.T) {
	terms := &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 15}

	current, _ := billingCycles(terms, date(2026, time.October, 10))
	if !current.start.Equal(date(2026, time.September, 15)) || !current.periodEnd.Equal(date(2026, time.October, 15)) {
		t.Errorf("before the invoice day: %v - %v", current.start, current.periodEnd)
	}

	current, _ = billingCycles(terms, date(2026, time.October, 20))
	if !current.start.Equal(date(2026, time.October, 15)) {
		t.Errorf("after the invoice day: %v", current.start)
	}
}

func TestBillingCyclesQuarterly(t *testing.T) {
	terms := &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Quarterly", InvoiceDayOfMonth: 1}
	current, previous := billingCycles(terms, date(2026, time.October, 21))
	if !current.start.Equal(date(2026, time.October, 1)) || !current.periodEnd.Equal(date(2027, time.January, 1)) {
		t.Errorf("current = %v - %v", current.start, current.periodEnd)
	}
	if !previous.start.Equal(date(2026, time.July, 1)) {
		t.Errorf("previous starts %v", previous.start)
	}
}

func TestRangeLabel(t *testing.T) {
	if got := rangeLabel(date(2026, time.September, 30), date(2026, time.October, 30)); got != "30 Sep - 30 Oct 2026" {
		t.Errorf("got %q", got)
	}
	if got := rangeLabel(date(2026, time.December, 15), date(2027, time.January, 15)); got != "15 Dec 2026 - 15 Jan 2027" {
		t.Errorf("got %q", got)
	}
}
