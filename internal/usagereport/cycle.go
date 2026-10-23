// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// Window is the time range a query reads. end is capped at "now" for an
// in-progress cycle; periodEnd is where the cycle actually ends.
type Window struct {
	// Name says which window this is, e.g. "current billing cycle".
	Name string
	// Start and End are the range a query reads; End is capped at now for
	// an in-progress cycle.
	Start time.Time
	End   time.Time
	// PeriodEnd is where the cycle actually ends.
	PeriodEnd time.Time
}

var frequencyMonths = map[string]int{"Monthly": 1, "Quarterly": 3, "Annual": 12}

// BillingCycles returns the current and previous billing cycles for a
// billing account's payment terms, the same windows the portal's cycle
// picker offers. Without terms it uses calendar months.
func BillingCycles(terms *billingv1alpha1.PaymentTerms, now time.Time) (current, previous Window) {
	day, months := 1, 1
	if terms != nil {
		if terms.InvoiceDayOfMonth > 0 {
			day = int(terms.InvoiceDayOfMonth)
		}
		if m, ok := frequencyMonths[terms.InvoiceFrequency]; ok {
			months = m
		}
	}

	now = now.UTC()
	start := anchorDate(now.Year(), now.Month(), day)
	for start.After(now) {
		start = addMonths(start, -months, day)
	}
	for !addMonths(start, months, day).After(now) {
		start = addMonths(start, months, day)
	}
	periodEnd := addMonths(start, months, day)
	previousStart := addMonths(start, -months, day)

	current = Window{Name: "current billing cycle", Start: start, End: minTime(periodEnd, now), PeriodEnd: periodEnd}
	previous = Window{Name: "previous billing cycle", Start: previousStart, End: start, PeriodEnd: start}
	return current, previous
}

// anchorDate is the invoice day in a month, clamped to the month's length
// (the API allows days 1-28, but stay safe for any value).
func anchorDate(year int, month time.Month, day int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func addMonths(t time.Time, months, day int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	return anchorDate(first.Year(), first.Month(), day)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// RangeLabel renders a Window like "30 Sep - 30 Oct 2026".
func RangeLabel(start, end time.Time) string {
	if start.Year() == end.Year() {
		return start.Format("2 Jan") + " - " + end.Format("2 Jan 2006")
	}
	return start.Format("2 Jan 2006") + " - " + end.Format("2 Jan 2006")
}
