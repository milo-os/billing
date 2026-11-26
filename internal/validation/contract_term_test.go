// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func termContract(startsAt time.Time, term *billingv1alpha1.ContractTerm) *billingv1alpha1.BillingContract {
	return &billingv1alpha1.BillingContract{StartsAt: metav1.NewTime(startsAt), Term: term}
}

func TestContractTermEndsAt(t *testing.T) {
	tests := []struct {
		name     string
		contract *billingv1alpha1.BillingContract
		want     time.Time
	}{
		{"no term", termContract(date(2026, time.April, 18), nil), time.Time{}},
		{"36 months on the start day", termContract(date(2026, time.April, 18), &billingv1alpha1.ContractTerm{Months: 36}), date(2029, time.April, 18)},
		{"starting on the 31st ends on the last day of a shorter month",
			termContract(date(2026, time.January, 31), &billingv1alpha1.ContractTerm{Months: 1}), date(2026, time.February, 28)},
		{"invoice day counts from the first billing date", &billingv1alpha1.BillingContract{
			StartsAt:   metav1.NewTime(date(2026, time.April, 18)),
			BillingDay: billingv1alpha1.ContractBillingDayInvoiceDay,
			Term:       &billingv1alpha1.ContractTerm{Months: 12},
		}, date(2027, time.May, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContractTermEndsAt(tt.contract, 1); !got.Equal(tt.want) {
				t.Errorf("ContractTermEndsAt() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestContractTermAt(t *testing.T) {
	start := date(2026, time.April, 18)
	monthToMonth := termContract(start, &billingv1alpha1.ContractTerm{Months: 36})
	renew := termContract(start, &billingv1alpha1.ContractTerm{Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndRenew, RenewMonths: 12})
	stop := termContract(start, &billingv1alpha1.ContractTerm{
		Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop, OfferAfterRef: &billingv1alpha1.OfferReference{Name: "payg"},
	})
	stopsAt := metav1.NewTime(date(2029, time.August, 18))
	stopLater := termContract(start, &billingv1alpha1.ContractTerm{
		Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop, OfferAfterRef: &billingv1alpha1.OfferReference{Name: "payg"},
		StopsAt: &stopsAt,
	})

	tests := []struct {
		name       string
		contract   *billingv1alpha1.BillingContract
		now        time.Time
		want       ContractTermState
		wantReason string
		wantDue    bool
	}{
		{
			name: "before the contract starts", contract: monthToMonth, now: date(2026, time.March, 1),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseInTerm, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), RenewalDueAt: date(2029, time.January, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonInTerm,
		},
		{
			name: "within 90 days of the end", contract: monthToMonth, now: date(2029, time.January, 18),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseInTerm, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), RenewalDueAt: date(2029, time.January, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonTermEndingSoon, wantDue: true,
		},
		{
			name: "month to month from the end", contract: monthToMonth, now: date(2029, time.April, 18),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseMonthToMonth, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), RenewalDueAt: date(2029, time.January, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonMonthToMonth, wantDue: true,
		},
		{
			name: "renewed twice, counted from the start", contract: renew, now: date(2030, time.May, 1),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseInTerm, CurrentTermStartsAt: date(2030, time.April, 18),
				EndsAt: date(2031, time.April, 18), Renewals: 2, RenewalDueAt: date(2031, time.January, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonInTerm,
		},
		{
			name: "set to stop, in its term", contract: stop, now: date(2029, time.February, 1),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseInTerm, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), RenewalDueAt: date(2029, time.January, 18),
				StopsAt: date(2029, time.April, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonTermEndingSoon, wantDue: true,
		},
		{
			name: "stopped at the term's end", contract: stop, now: date(2029, time.April, 18),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseStopped, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), StopsAt: date(2029, time.April, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopped,
		},
		{
			name: "month to month until stopsAt", contract: stopLater, now: date(2029, time.June, 1),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseMonthToMonth, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.April, 18), RenewalDueAt: date(2029, time.January, 18),
				StopsAt: date(2029, time.August, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonMonthToMonth, wantDue: true,
		},
		{
			name: "stopped at stopsAt", contract: stopLater, now: date(2029, time.August, 18),
			want: ContractTermState{
				Phase: billingv1alpha1.ContractTermPhaseStopped, CurrentTermStartsAt: start,
				EndsAt: date(2029, time.August, 18), StopsAt: date(2029, time.August, 18),
			},
			wantReason: billingv1alpha1.ContractRenewalDueReasonStopped,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ContractTermAt(tt.contract, 1, tt.now)
			if !ok {
				t.Fatal("ContractTermAt() ok = false, want true")
			}
			if got != tt.want {
				t.Errorf("ContractTermAt() = %+v, want %+v", got, tt.want)
			}
			reason, due := ContractRenewalDue(got, ok, tt.now)
			if reason != tt.wantReason || due != tt.wantDue {
				t.Errorf("ContractRenewalDue() = %s, %v, want %s, %v", reason, due, tt.wantReason, tt.wantDue)
			}
		})
	}

	if _, ok := ContractTermAt(termContract(start, nil), 1, start); ok {
		t.Error("ContractTermAt() without a term: ok = true, want false")
	}
	if reason, due := ContractRenewalDue(ContractTermState{}, false, start); reason != billingv1alpha1.ContractRenewalDueReasonNoTerm || due {
		t.Errorf("ContractRenewalDue() without a term = %s, %v", reason, due)
	}
}

func TestNextContractTermBoundary(t *testing.T) {
	contract := termContract(date(2026, time.April, 18), &billingv1alpha1.ContractTerm{Months: 36})
	for _, tt := range []struct {
		now, want time.Time
	}{
		{date(2027, time.June, 1), date(2029, time.January, 18)},
		{date(2029, time.February, 1), date(2029, time.April, 18)},
		{date(2029, time.May, 1), time.Time{}},
	} {
		state, ok := ContractTermAt(contract, 1, tt.now)
		if got := NextContractTermBoundary(state, ok, tt.now); !got.Equal(tt.want) {
			t.Errorf("NextContractTermBoundary(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

func TestIsContractBillingDate(t *testing.T) {
	startDate := termContract(date(2026, time.April, 18), nil)
	invoiceDay := &billingv1alpha1.BillingContract{
		StartsAt:   metav1.NewTime(date(2026, time.April, 18)),
		BillingDay: billingv1alpha1.ContractBillingDayInvoiceDay,
	}
	monthEnd := termContract(date(2026, time.January, 31), nil)
	tests := []struct {
		name     string
		contract *billingv1alpha1.BillingContract
		interval billingv1alpha1.ChargeInterval
		t        time.Time
		want     bool
	}{
		{"monthly on the start day", startDate, billingv1alpha1.ChargeIntervalMonthly, date(2027, time.June, 18), true},
		{"monthly off the start day", startDate, billingv1alpha1.ChargeIntervalMonthly, date(2027, time.June, 19), false},
		{"annual on the anniversary", startDate, billingv1alpha1.ChargeIntervalAnnual, date(2028, time.April, 18), true},
		{"annual partway through a year", startDate, billingv1alpha1.ChargeIntervalAnnual, date(2028, time.October, 18), false},
		{"quarterly on a quarter", startDate, billingv1alpha1.ChargeIntervalQuarterly, date(2026, time.October, 18), true},
		{"invoice day monthly", invoiceDay, billingv1alpha1.ChargeIntervalMonthly, date(2026, time.June, 1), true},
		{"invoice day quarterly on a calendar quarter", invoiceDay, billingv1alpha1.ChargeIntervalQuarterly, date(2026, time.October, 1), true},
		{"invoice day quarterly off a calendar quarter", invoiceDay, billingv1alpha1.ChargeIntervalQuarterly, date(2026, time.August, 1), false},
		{"clamped to February's last day", monthEnd, billingv1alpha1.ChargeIntervalMonthly, date(2026, time.February, 28), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContractBillingDate(tt.contract, 1, tt.interval, tt.t); got != tt.want {
				t.Errorf("IsContractBillingDate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextContractBillingDate(t *testing.T) {
	contract := termContract(date(2026, time.April, 18), nil)
	for _, tt := range []struct {
		now, want time.Time
	}{
		{date(2026, time.March, 1), date(2026, time.April, 18)},
		{date(2026, time.April, 18), date(2026, time.May, 18)},
		{date(2027, time.June, 17), date(2027, time.June, 18)},
		{date(2027, time.June, 20), date(2027, time.July, 18)},
	} {
		if got := NextContractBillingDate(contract, 1, tt.now); !got.Equal(tt.want) {
			t.Errorf("NextContractBillingDate(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

// termTestClient serves the Offers the term validation tests read: the
// contract's, with a monthly fee in advance, an annual fee in advance and a
// quarterly fee in arrears; one with a fee for 48 months; a pay-as-you-go
// Offer; and one with a one-time fee.
func termTestClient(t *testing.T) client.Client {
	t.Helper()
	recurring := func(name string, interval billingv1alpha1.ChargeInterval, timing billingv1alpha1.BillingTiming, periods int32) billingv1alpha1.ServicePricingSnapshot {
		return billingv1alpha1.ServicePricingSnapshot{
			Name:          name,
			BillingTiming: timing,
			Periods:       periods,
			Spec:          billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeRecurring, Interval: interval},
		}
	}
	offer := func(name string, pricings ...billingv1alpha1.ServicePricingSnapshot) *billingv1alpha1.Offer {
		return &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       billingv1alpha1.OfferSpec{LaunchStage: billingv1alpha1.OfferLaunchStageGA, ServicePricings: pricings},
		}
	}
	usage := billingv1alpha1.ServicePricingSnapshot{Name: "usage", Spec: billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeUsage}}
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(
		offer("contract",
			recurring("servers", billingv1alpha1.ChargeIntervalMonthly, billingv1alpha1.BillingTimingInAdvance, 0),
			recurring("rack", billingv1alpha1.ChargeIntervalAnnual, billingv1alpha1.BillingTimingInAdvance, 0),
			recurring("support", billingv1alpha1.ChargeIntervalQuarterly, billingv1alpha1.BillingTimingInArrears, 0),
		),
		offer("lease", recurring("lease", billingv1alpha1.ChargeIntervalMonthly, billingv1alpha1.BillingTimingInAdvance, 48)),
		offer("payg", usage),
		offer("setup", usage, billingv1alpha1.ServicePricingSnapshot{
			Name: "setup-fee", Spec: billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeOneTime},
		}),
	).Build()
}

func termEntitlement(offer string, term *billingv1alpha1.ContractTerm) *billingv1alpha1.BillingEntitlement {
	return &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "be-acme"},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			OfferRef:          billingv1alpha1.OfferReference{Name: offer},
			Contract:          termContract(date(2026, time.April, 18), term),
		},
	}
}

func TestValidateBillingEntitlementUpdate_Term(t *testing.T) {
	payg := &billingv1alpha1.OfferReference{Name: "payg"}
	stopsAt := func(t time.Time) *metav1.Time { m := metav1.NewTime(t); return &m }
	stop := func(months int32, at *metav1.Time) *billingv1alpha1.ContractTerm {
		return &billingv1alpha1.ContractTerm{Months: months, AtEnd: billingv1alpha1.ContractTermAtEndStop, OfferAfterRef: payg, StopsAt: at}
	}
	inTerm := date(2027, time.June, 20)
	monthToMonth := date(2029, time.June, 20)

	tests := []struct {
		name     string
		now      time.Time
		oldOffer string
		oldTerm  *billingv1alpha1.ContractTerm
		newOffer string
		newTerm  *billingv1alpha1.ContractTerm
		wantErr  string
	}{
		{name: "adding a term", now: inTerm, newTerm: &billingv1alpha1.ContractTerm{Months: 36}},
		{name: "a term that isn't a whole number of years with an annual fee", now: inTerm,
			newTerm: &billingv1alpha1.ContractTerm{Months: 30}, wantErr: "rack is billed annual"},
		{name: "renewals that aren't a whole number of years", now: inTerm,
			newTerm: &billingv1alpha1.ContractTerm{Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndRenew, RenewMonths: 6},
			wantErr: "spec.contract.term.renewMonths"},
		{name: "shortening a term into the past", now: inTerm, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: &billingv1alpha1.ContractTerm{Months: 12}, wantErr: "must end on or after the contract's next billing date, 18 July 2027"},
		{name: "a new term while month to month", now: monthToMonth, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: &billingv1alpha1.ContractTerm{Months: 48}},
		{name: "renewing while month to month", now: monthToMonth, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: &billingv1alpha1.ContractTerm{Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndRenew, RenewMonths: 12}},
		{name: "stopping at the term's end", now: inTerm, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: stop(36, nil)},
		{name: "stopsAt in the term", now: inTerm, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: stop(36, stopsAt(date(2029, time.April, 18))), wantErr: "only allowed once the term has ended"},
		{name: "stopping month to month without stopsAt", now: monthToMonth, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: stop(36, nil), wantErr: "say when the contract stops"},
		{name: "stopping month to month at the end of the annual fee's year", now: monthToMonth,
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, stopsAt(date(2030, time.April, 18)))},
		{name: "stopping month to month partway through the annual fee's year", now: monthToMonth,
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, stopsAt(date(2029, time.August, 18))),
			wantErr: "rack is billed annual in advance"},
		{name: "stopping without an annual fee on the next billing date", now: monthToMonth, oldOffer: "payg", newOffer: "payg",
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, stopsAt(date(2029, time.July, 18)))},
		{name: "stopping in the month already billed", now: monthToMonth, oldOffer: "payg", newOffer: "payg",
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, stopsAt(date(2029, time.June, 18))),
			wantErr: "on or after the next one, 18 July 2029"},
		{name: "stopping off a billing date", now: monthToMonth, oldOffer: "payg", newOffer: "payg",
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, stopsAt(date(2029, time.July, 20))),
			wantErr: "one of the contract's billing dates"},
		{name: "stopping before a fee's periods end", now: inTerm, oldOffer: "lease", newOffer: "lease",
			oldTerm: &billingv1alpha1.ContractTerm{Months: 36}, newTerm: stop(36, nil), wantErr: "lease runs until 18 April 2030"},
		{name: "moving onto an Offer with one-time fees", now: inTerm, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: &billingv1alpha1.ContractTerm{Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop,
				OfferAfterRef: &billingv1alpha1.OfferReference{Name: "setup"}}, wantErr: "charges one-time fees"},
		{name: "moving onto a missing Offer", now: inTerm, oldTerm: &billingv1alpha1.ContractTerm{Months: 36},
			newTerm: &billingv1alpha1.ContractTerm{Months: 36, AtEnd: billingv1alpha1.ContractTermAtEndStop,
				OfferAfterRef: &billingv1alpha1.OfferReference{Name: "nope"}}, wantErr: "doesn't exist"},
		{name: "changing a stopped contract's term", now: monthToMonth, oldOffer: "payg", newOffer: "payg",
			oldTerm: stop(36, nil), newTerm: &billingv1alpha1.ContractTerm{Months: 48}, wantErr: "stopped on 18 April 2029"},
		{name: "billing moving a stopped contract onto offerAfterRef", now: monthToMonth,
			oldTerm: stop(36, nil), newOffer: "payg", newTerm: stop(36, nil)},
		{name: "an amendment adding an annual fee to a term that isn't a whole number of years", now: inTerm,
			oldOffer: "payg", oldTerm: &billingv1alpha1.ContractTerm{Months: 30},
			newTerm: &billingv1alpha1.ContractTerm{Months: 30}, wantErr: "rack is billed annual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldOffer, newOffer := tt.oldOffer, tt.newOffer
			if oldOffer == "" {
				oldOffer = "contract"
			}
			if newOffer == "" {
				newOffer = "contract"
			}
			oldBE := termEntitlement(oldOffer, tt.oldTerm)
			newBE := termEntitlement(newOffer, tt.newTerm)
			errs := ValidateBillingEntitlementUpdate(context.Background(), termTestClient(t), oldBE, newBE, tt.now)
			switch {
			case tt.wantErr == "" && len(errs) > 0:
				t.Errorf("ValidateBillingEntitlementUpdate() = %v, want no errors", errs)
			case tt.wantErr != "" && !strings.Contains(errs.ToAggregate().Error(), tt.wantErr):
				t.Errorf("ValidateBillingEntitlementUpdate() = %v, want an error containing %q", errs, tt.wantErr)
			}
		})
	}
}
