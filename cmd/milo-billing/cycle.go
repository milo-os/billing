package main

import (
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// window is the time range a query reads. end is capped at "now" for an
// in-progress cycle; periodEnd is where the cycle actually ends.
type window struct {
	name      string
	start     time.Time
	end       time.Time
	periodEnd time.Time
}

var frequencyMonths = map[string]int{"Monthly": 1, "Quarterly": 3, "Annual": 12}

// billingCycles returns the current and previous billing cycles for a
// billing account's payment terms, the same windows the portal's cycle
// picker offers. Without terms it uses calendar months.
func billingCycles(terms *billingv1alpha1.PaymentTerms, now time.Time) (current, previous window) {
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

	current = window{name: "current billing cycle", start: start, end: minTime(periodEnd, now), periodEnd: periodEnd}
	previous = window{name: "previous billing cycle", start: previousStart, end: start, periodEnd: start}
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

// rangeLabel renders a window like "30 Sep - 30 Oct 2026".
func rangeLabel(start, end time.Time) string {
	if start.Year() == end.Year() {
		return start.Format("2 Jan") + " - " + end.Format("2 Jan 2006")
	}
	return start.Format("2 Jan 2006") + " - " + end.Format("2 Jan 2006")
}
