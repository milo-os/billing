// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"fmt"
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// WindowOptions picks the time range a report covers: a billing cycle, or a
// custom range when From is set.
type WindowOptions struct {
	// Cycle is "current" (the default) or "previous".
	Cycle string
	// From and To are dates (YYYY-MM-DD) or RFC 3339 times; To defaults to now.
	From string
	To   string
}

// InputError is a problem with what the caller asked for, not with the API.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErrorf(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// ResolveWindow turns WindowOptions into a time range. terms is only called
// for a billing cycle, so a custom range costs no billing-account reads.
func ResolveWindow(opts WindowOptions, terms func() *billingv1alpha1.PaymentTerms, now time.Time) (Window, error) {
	if opts.From != "" {
		start, err := ParseTime(opts.From)
		if err != nil {
			return Window{}, inputErrorf("invalid from: %v", err)
		}
		end := now.UTC()
		if opts.To != "" {
			if end, err = ParseTime(opts.To); err != nil {
				return Window{}, inputErrorf("invalid to: %v", err)
			}
		}
		if !end.After(start) {
			return Window{}, inputErrorf("to must be after from")
		}
		return Window{Name: "custom range", Start: start, End: end, PeriodEnd: end}, nil
	}
	if opts.To != "" {
		return Window{}, inputErrorf("to needs from")
	}

	switch opts.Cycle {
	case "", "current":
		current, _ := BillingCycles(terms(), now)
		return current, nil
	case "previous":
		_, previous := BillingCycles(terms(), now)
		return previous, nil
	}
	return Window{}, inputErrorf("unknown cycle %q; use current or previous", opts.Cycle)
}

// ParseTime reads a date (YYYY-MM-DD) or an RFC 3339 time, in UTC.
func ParseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date (YYYY-MM-DD) or RFC 3339 time", v)
	}
	return t.UTC(), nil
}
