// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// contractFor returns acme's entitlement with a 12-month contract starting
// on 1 October 2026, set to stop when stop is true, so it stops on 1
// October 2027.
func contractFor(stop bool) *billingv1alpha1.BillingEntitlement {
	term := &billingv1alpha1.ContractTerm{Months: 12}
	if stop {
		term.AtEnd = billingv1alpha1.ContractTermAtEndStop
		term.OfferAfterRef = &billingv1alpha1.OfferReference{Name: "payg"}
	}
	return &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			OfferRef:          billingv1alpha1.OfferReference{Name: "acme-contract"},
			Contract: &billingv1alpha1.BillingContract{
				StartsAt: metav1.NewTime(time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)),
				Term:     term,
			},
		},
	}
}

func tiedArrangement(name string, startsAt, endsAt *metav1.Time) *billingv1alpha1.BillingArrangement {
	arr := invoiceArrangement(name, startsAt, endsAt)
	arr.Spec.EndsWithContract = true
	return arr
}

func TestArrangementEnd(t *testing.T) {
	stop := time.Date(2027, time.October, 1, 0, 0, 0, 0, time.UTC)
	earlier := metav1.NewTime(stop.AddDate(0, -1, 0))
	later := metav1.NewTime(stop.AddDate(0, 1, 0))
	tests := []struct {
		name string
		arr  *billingv1alpha1.BillingArrangement
		stop time.Time
		want *time.Time
	}{
		{"untied with no end", invoiceArrangement("a", nil, nil), stop, nil},
		{"untied ignores the contract", invoiceArrangement("a", nil, &later), stop, &later.Time},
		{"tied while the contract carries on", tiedArrangement("a", nil, nil), time.Time{}, nil},
		{"tied ends with the contract", tiedArrangement("a", nil, nil), stop, &stop},
		{"tied ends at endsAt when that's sooner", tiedArrangement("a", nil, &earlier), stop, &earlier.Time},
		{"tied ends with the contract when that's sooner", tiedArrangement("a", nil, &later), stop, &stop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ArrangementEnd(tt.arr, tt.stop)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("ArrangementEnd() = %s, want no end", got.Time)
			case tt.want != nil && (got == nil || !got.Time.Equal(*tt.want)):
				t.Errorf("ArrangementEnd() = %v, want %s", got, *tt.want)
			}
		})
	}
}

func TestValidateTiedArrangement(t *testing.T) {
	now := arrangementTime(6).Time
	afterStop := metav1.NewTime(time.Date(2027, time.October, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name     string
		existing []client.Object
		arr      *billingv1alpha1.BillingArrangement
		wantErr  string
	}{
		{name: "tied to the account's contract", existing: []client.Object{contractFor(false)},
			arr: tiedArrangement("tied", nil, nil)},
		{name: "tied without a contract", arr: tiedArrangement("tied", nil, nil),
			wantErr: "has no contract for the terms to end with"},
		{name: "scheduled after tied terms that carry on", existing: []client.Object{
			contractFor(false), tiedArrangement("tied", arrangementTime(1), nil)},
			arr: invoiceArrangement("next", &afterStop, nil), wantErr: `overlaps arrangement "tied"`},
		{name: "scheduled after tied terms, from the contract's stop", existing: []client.Object{
			contractFor(true), tiedArrangement("tied", arrangementTime(1), nil)},
			arr: invoiceArrangement("next", &afterStop, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateBillingArrangementCreate(context.Background(), arrangementTestClient(t, tt.existing...), tt.arr, now)
			switch {
			case tt.wantErr == "" && len(errs) > 0:
				t.Errorf("ValidateBillingArrangementCreate() = %v, want no errors", errs)
			case tt.wantErr != "" && !strings.Contains(errs.ToAggregate().Error(), tt.wantErr):
				t.Errorf("ValidateBillingArrangementCreate() = %v, want an error containing %q", errs, tt.wantErr)
			}
		})
	}

	t.Run("tying existing terms without a contract", func(t *testing.T) {
		old := invoiceArrangement("terms", arrangementTime(1), nil)
		errs := ValidateBillingArrangementUpdate(context.Background(), arrangementTestClient(t, old), old,
			tiedArrangement("terms", arrangementTime(1), nil), now)
		if !strings.Contains(errs.ToAggregate().Error(), "has no contract") {
			t.Errorf("ValidateBillingArrangementUpdate() = %v", errs)
		}
	})
	t.Run("tying existing terms to the contract", func(t *testing.T) {
		old := invoiceArrangement("terms", arrangementTime(1), nil)
		if errs := ValidateBillingArrangementUpdate(context.Background(), arrangementTestClient(t, old, contractFor(false)), old,
			tiedArrangement("terms", arrangementTime(1), nil), now); len(errs) > 0 {
			t.Errorf("ValidateBillingArrangementUpdate() = %v", errs)
		}
	})
}

func TestTyingTermsToAStoppedContract(t *testing.T) {
	afterStop := time.Date(2027, time.November, 1, 0, 0, 0, 0, time.UTC)
	errs := ValidateBillingArrangementCreate(context.Background(), arrangementTestClient(t, contractFor(true)),
		tiedArrangement("tied", nil, nil), afterStop)
	if !strings.Contains(errs.ToAggregate().Error(), "contract stopped on 1 October 2027") {
		t.Errorf("create = %v, want the stopped contract refused", errs)
	}

	old := invoiceArrangement("terms", arrangementTime(1), nil)
	errs = ValidateBillingArrangementUpdate(context.Background(), arrangementTestClient(t, old, contractFor(true)), old,
		tiedArrangement("terms", arrangementTime(1), nil), afterStop)
	if !strings.Contains(errs.ToAggregate().Error(), "contract stopped on 1 October 2027") {
		t.Errorf("update = %v, want the stopped contract refused", errs)
	}

	// Terms already tied can still be edited, and ended, after the stop.
	tied := tiedArrangement("terms", arrangementTime(1), nil)
	edited := tied.DeepCopy()
	edited.Spec.Invoice.NetDays = 45
	if errs := ValidateBillingArrangementUpdate(context.Background(), arrangementTestClient(t, tied, contractFor(true)), tied,
		edited, afterStop); len(errs) > 0 {
		t.Errorf("editing tied terms after the stop = %v", errs)
	}
}

// TestTiedTermsUseTheAccountsInvoiceDay edits terms scheduled after the
// stop of a contract billed on the account's invoice day, the 15th: the tied
// terms end on 15 October 2027, not on the 1st as with the standard day.
func TestTiedTermsUseTheAccountsInvoiceDay(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingAccountSpec{
			CurrencyCode: "USD",
			PaymentTerms: &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 15, NetDays: 30},
		},
	}
	be := contractFor(true)
	be.Spec.Contract.BillingDay = billingv1alpha1.ContractBillingDayInvoiceDay
	tied := tiedArrangement("tied", arrangementTime(1), nil)
	at := func(month time.Month, day int) *metav1.Time {
		t := metav1.NewTime(time.Date(2027, month, day, 0, 0, 0, 0, time.UTC))
		return &t
	}
	next := invoiceArrangement("next", at(time.November, 15), nil)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account, be, tied, next).Build()
	now := arrangementTime(6).Time

	before := next.DeepCopy()
	before.Spec.StartsAt = at(time.October, 10)
	if errs := ValidateBillingArrangementUpdate(context.Background(), cl, next, before, now); !strings.Contains(errs.ToAggregate().Error(), `overlaps arrangement "tied"`) {
		t.Errorf("moving the next terms before the stop = %v, want an overlap", errs)
	}
	atStop := next.DeepCopy()
	atStop.Spec.StartsAt = at(time.October, 15)
	if errs := ValidateBillingArrangementUpdate(context.Background(), cl, next, atStop, now); len(errs) > 0 {
		t.Errorf("moving the next terms to the stop = %v, want them allowed", errs)
	}
}

func TestTiedTermsIgnoreAnotherAccountsContract(t *testing.T) {
	old := contractFor(false)
	old.OwnerReferences = []metav1.OwnerReference{{APIVersion: "billing.miloapis.com/v1alpha1", Kind: "BillingAccount", Name: "acme", UID: "deleted"}}
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "current"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account, old).Build()
	errs := ValidateBillingArrangementCreate(context.Background(), cl, tiedArrangement("tied", nil, nil), arrangementTime(6).Time)
	if !strings.Contains(errs.ToAggregate().Error(), "has no contract") {
		t.Errorf("create = %v, want the old account's contract ignored", errs)
	}
}

func TestTiedTermsReportListErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	old := invoiceArrangement("terms", arrangementTime(1), nil)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(old).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*billingv1alpha1.BillingEntitlementList); ok {
					return errors.New("apiserver unavailable")
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()
	edited := old.DeepCopy()
	edited.Spec.Invoice.NetDays = 45
	errs := ValidateBillingArrangementUpdate(context.Background(), cl, old, edited, arrangementTime(6).Time)
	if !strings.Contains(errs.ToAggregate().Error(), "apiserver unavailable") {
		t.Errorf("update = %v, want the listing error reported", errs)
	}
}

func TestContractChangesKeepTiedTermsApart(t *testing.T) {
	stop := metav1.NewTime(time.Date(2027, time.October, 1, 0, 0, 0, 0, time.UTC))
	tied := tiedArrangement("tied", arrangementTime(1), nil)
	next := invoiceArrangement("next", &stop, nil)
	stopping, carryingOn := contractFor(true), contractFor(false)
	oldStop := ContractStopDate(stopping.Spec.Contract, 1)
	termPath := field.NewPath("spec", "contract", "term")

	errs := validateTiedTermsStillFit(context.Background(), arrangementTestClient(t, stopping, tied, next),
		carryingOn, oldStop, time.Time{}, termPath)
	if !strings.Contains(errs.ToAggregate().Error(), `payment terms "tied" end with the contract, so they'd overlap payment terms "next"`) {
		t.Errorf("no longer stopping = %v, want the overlap refused", errs)
	}
	later := oldStop.AddDate(0, 1, 0)
	if errs := validateTiedTermsStillFit(context.Background(), arrangementTestClient(t, stopping, tied, next),
		stopping, oldStop, later, termPath); len(errs) == 0 {
		t.Error("stopping a month later: no overlap reported")
	}
	if errs := validateTiedTermsStillFit(context.Background(), arrangementTestClient(t, stopping, tied),
		carryingOn, oldStop, time.Time{}, termPath); len(errs) > 0 {
		t.Errorf("no longer stopping, with nothing scheduled after = %v", errs)
	}
}
