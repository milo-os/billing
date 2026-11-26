// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ContractRenewalNoticeDays is how many days before a contract's term ends
// the account is flagged for renewal, so staff can agree what comes next.
const ContractRenewalNoticeDays = 90

// ContractTermState is where a contract is in its term at a point in time.
// Billing works it out from the contract and the time alone, so nothing
// records which term a contract is in.
type ContractTermState struct {
	// Phase is InTerm, MonthToMonth or Stopped.
	Phase billingv1alpha1.ContractTermPhase
	// CurrentTermStartsAt is when the current (or, month to month and
	// stopped, the last) term started.
	CurrentTermStartsAt time.Time
	// EndsAt is when the current term ends; month to month, when the last
	// one ended; stopped, when the contract stopped.
	EndsAt time.Time
	// Renewals counts the renewals so far, for a contract that renews.
	Renewals int32
	// RenewalDueAt is ContractRenewalNoticeDays before EndsAt. Zero once
	// stopped.
	RenewalDueAt time.Time
	// StopsAt is the stop date of a contract set to stop that hasn't yet,
	// and when it stopped once it has. Zero otherwise.
	StopsAt time.Time
}

// ContractTermAtEnd returns what happens when term ends, with the default
// filled in.
func ContractTermAtEnd(term *billingv1alpha1.ContractTerm) billingv1alpha1.ContractTermAtEnd {
	if term == nil || term.AtEnd == "" {
		return billingv1alpha1.ContractTermAtEndMonthToMonth
	}
	return term.AtEnd
}

// ContractRenewMonths returns the length of each renewal of term: its
// renewMonths, or months when that's unset.
func ContractRenewMonths(term *billingv1alpha1.ContractTerm) int32 {
	if term.RenewMonths > 0 {
		return term.RenewMonths
	}
	return term.Months
}

// contractTermEndAfter returns when a contract's term ends after months
// months, counted as a monthly fee's periods are.
func contractTermEndAfter(contract *billingv1alpha1.BillingContract, invoiceDay int32, months int32) time.Time {
	return ContractFeeEndsAt(contract, invoiceDay, billingv1alpha1.ChargeIntervalMonthly, months)
}

// ContractTermEndsAt returns when a contract's first term ends, or the
// zero time when it has no term.
func ContractTermEndsAt(contract *billingv1alpha1.BillingContract, invoiceDay int32) time.Time {
	if contract == nil || contract.Term == nil {
		return time.Time{}
	}
	return contractTermEndAfter(contract, invoiceDay, contract.Term.Months)
}

// ContractStopDate returns when a contract set to stop stops: its stopsAt
// once it's month to month, otherwise its term's end. It's the zero time
// for a contract that isn't set to stop.
func ContractStopDate(contract *billingv1alpha1.BillingContract, invoiceDay int32) time.Time {
	if contract == nil || contract.Term == nil || ContractTermAtEnd(contract.Term) != billingv1alpha1.ContractTermAtEndStop {
		return time.Time{}
	}
	if contract.Term.StopsAt != nil {
		return contract.Term.StopsAt.UTC()
	}
	return ContractTermEndsAt(contract, invoiceDay)
}

// ContractStopped reports whether a contract set to stop has stopped by
// now.
func ContractStopped(contract *billingv1alpha1.BillingContract, invoiceDay int32, now time.Time) bool {
	stop := ContractStopDate(contract, invoiceDay)
	return !stop.IsZero() && !now.Before(stop)
}

// ContractTermAt works out where a contract is in its term at now. It
// returns false for a contract without a term. invoiceDay is the
// account's, which a contract billed on the invoice day counts its term
// from.
//
// Renewals are counted from the contract's start, not chained from one
// another, so each ends on the contract's billing day: a 36-month term
// renewing for 12 months ends after 36, 48, 60 months and so on.
func ContractTermAt(contract *billingv1alpha1.BillingContract, invoiceDay int32, now time.Time) (ContractTermState, bool) {
	if contract == nil || contract.Term == nil || contract.Term.Months <= 0 {
		return ContractTermState{}, false
	}
	term := contract.Term
	state := ContractTermState{
		Phase:               billingv1alpha1.ContractTermPhaseInTerm,
		CurrentTermStartsAt: contract.StartsAt.UTC(),
		EndsAt:              ContractTermEndsAt(contract, invoiceDay),
	}

	switch ContractTermAtEnd(term) {
	case billingv1alpha1.ContractTermAtEndRenew:
		renewMonths := ContractRenewMonths(term)
		for !now.Before(state.EndsAt) {
			state.Renewals++
			state.CurrentTermStartsAt = state.EndsAt
			state.EndsAt = contractTermEndAfter(contract, invoiceDay, term.Months+state.Renewals*renewMonths)
		}
	case billingv1alpha1.ContractTermAtEndStop:
		state.StopsAt = ContractStopDate(contract, invoiceDay)
		switch {
		case !now.Before(state.StopsAt):
			state.Phase = billingv1alpha1.ContractTermPhaseStopped
			state.EndsAt = state.StopsAt
			return state, true
		case !now.Before(state.EndsAt):
			state.Phase = billingv1alpha1.ContractTermPhaseMonthToMonth
		}
	default:
		if !now.Before(state.EndsAt) {
			state.Phase = billingv1alpha1.ContractTermPhaseMonthToMonth
		}
	}
	state.RenewalDueAt = state.EndsAt.AddDate(0, 0, -ContractRenewalNoticeDays)
	return state, true
}

// ContractRenewalDue returns the ContractRenewalDue condition's reason for
// a contract's term state, and whether the condition is True. ok is false
// for a contract without a term, which has no condition: the reason is
// empty.
func ContractRenewalDue(state ContractTermState, ok bool, now time.Time) (reason string, due bool) {
	if !ok {
		return "", false
	}
	switch state.Phase {
	case billingv1alpha1.ContractTermPhaseStopped:
		return billingv1alpha1.ContractRenewalDueReasonStopped, false
	case billingv1alpha1.ContractTermPhaseMonthToMonth:
		return billingv1alpha1.ContractRenewalDueReasonMonthToMonth, true
	}
	if !now.Before(state.RenewalDueAt) {
		return billingv1alpha1.ContractRenewalDueReasonTermEndingSoon, true
	}
	return billingv1alpha1.ContractRenewalDueReasonInTerm, false
}

// NextContractTermBoundary returns the next time after now that a
// contract's term state or renewal flag can change: the current term's
// renewal date, its end or the stop date. It's the zero time when nothing
// changes again.
func NextContractTermBoundary(state ContractTermState, ok bool, now time.Time) time.Time {
	if !ok || state.Phase == billingv1alpha1.ContractTermPhaseStopped {
		return time.Time{}
	}
	var next time.Time
	for _, t := range []time.Time{state.RenewalDueAt, state.EndsAt, state.StopsAt} {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

// contractPeriodStart returns when a contract's fee charged every interval
// starts counting whole periods: the contract's start, or, for a contract
// billed on the invoice day, its first billing date for that interval.
func contractPeriodStart(contract *billingv1alpha1.BillingContract, invoiceDay int32,
	interval billingv1alpha1.ChargeInterval) time.Time {
	start := contract.StartsAt.UTC()
	if ContractBillsOnInvoiceDay(contract) {
		start = NextBillingDate(start, invoiceDay, interval)
	}
	return start
}

// IsContractBillingDate reports whether t is a date on which a contract's
// fee charged every interval bills: a whole number of periods after the
// fee starts counting them (see ContractFeeEndsAt).
func IsContractBillingDate(contract *billingv1alpha1.BillingContract, invoiceDay int32,
	interval billingv1alpha1.ChargeInterval, t time.Time) bool {
	start := contractPeriodStart(contract, invoiceDay, interval)
	t = t.UTC()
	if t.Equal(start) {
		return true
	}
	if t.Before(start) {
		return false
	}
	months := (t.Year()-start.Year())*12 + int(t.Month()) - int(start.Month())
	intervalMonths := IntervalMonths(interval)
	if months <= 0 || months%intervalMonths != 0 {
		return false
	}
	return FeeEndsAt(start, interval, int32(months/intervalMonths)).Equal(t)
}

// NextContractBillingDate returns a contract's next monthly billing date
// after now: the first date after now that a monthly fee bills on, or the
// contract's start before it starts. The month already billed runs until
// then.
func NextContractBillingDate(contract *billingv1alpha1.BillingContract, invoiceDay int32, now time.Time) time.Time {
	start := contract.StartsAt.UTC()
	if now.Before(start) {
		return start
	}
	periodStart := contractPeriodStart(contract, invoiceDay, billingv1alpha1.ChargeIntervalMonthly)
	if now.Before(periodStart) {
		return periodStart
	}
	months := (now.Year()-periodStart.Year())*12 + int(now.Month()) - int(periodStart.Month())
	for n := max(months, 1); ; n++ {
		if t := FeeEndsAt(periodStart, billingv1alpha1.ChargeIntervalMonthly, int32(n)); t.After(now) {
			return t
		}
	}
}
