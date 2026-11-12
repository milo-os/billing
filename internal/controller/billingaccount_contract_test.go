// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// fee is a fixed-fee entry of a published Offer's servicePricings.
func fee(name, displayName string, chargeType billingv1alpha1.ChargeType, amount string) billingv1alpha1.ServicePricingSnapshot {
	s := billingv1alpha1.ServicePricingSnapshot{
		Name: name,
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  chargeType,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			DisplayName: displayName,
			Amount:      amount,
		},
	}
	switch chargeType {
	case billingv1alpha1.ChargeTypeOneTime:
		s.Spec.Trigger = billingv1alpha1.ChargeTriggerBillingAccountActivation
	case billingv1alpha1.ChargeTypeRecurring:
		s.Spec.Interval = billingv1alpha1.ChargeIntervalMonthly
	}
	return s
}

func contractEntitlement(offer string, startsAt time.Time) *billingv1alpha1.BillingEntitlement {
	be := entitlement("acme", offer)
	be.Spec.Contract = &billingv1alpha1.BillingContract{StartsAt: metav1.NewTime(startsAt)}
	return be
}

func reconcileAccountContract(t *testing.T, objs ...client.Object) *billingv1alpha1.BillingAccountContract {
	t.Helper()
	cl := fake.NewClientBuilder().
		WithScheme(setupTestScheme(t)).
		WithObjects(objs...).
		WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
			return []string{obj.(*billingv1alpha1.BillingEntitlement).Spec.BillingAccountRef.Name}
		}).
		Build()

	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Status: billingv1alpha1.BillingAccountStatus{
			// A stale value the reconcile must replace or clear.
			Contract: &billingv1alpha1.BillingAccountContract{BillingDayOfMonth: 9},
		},
	}
	r := &BillingAccountReconciler{client: cl}
	if err := r.reconcileEntitlementSummary(context.Background(), account); err != nil {
		t.Fatalf("reconcileEntitlementSummary: %v", err)
	}
	return account.Status.Contract
}

func TestAccountContract(t *testing.T) {
	startsAt := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)
	contractOffer := publishedOffer("acme-contract", billingv1alpha1.OfferLaunchStageGA,
		fee("acme-setup", "Setup fee", billingv1alpha1.ChargeTypeOneTime, "5000.00"),
		fee("acme-monthly", "Monthly fee", billingv1alpha1.ChargeTypeRecurring, "2400.00"),
		snapshot("alb-requests", "ALB Requests", -1),
	)

	crossConnects := fee("cross-connect-300", "Cross connect", billingv1alpha1.ChargeTypeRecurring, "300.00")
	crossConnects.Spec.ServiceRef = ""
	crossConnects.Spec.PricingUnit = "port"
	crossConnects.Quantity = 4

	support := fee("support-annual", "Premium support", billingv1alpha1.ChargeTypeRecurring, "1200.00")
	support.Spec.Interval = billingv1alpha1.ChargeIntervalAnnual
	support.Periods = 3
	setup := fee("acme-setup", "Setup fee", billingv1alpha1.ChargeTypeOneTime, "5000.00")
	endsAt := metav1.NewTime(time.Date(2029, time.April, 18, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name        string
		entitlement *billingv1alpha1.BillingEntitlement
		offer       *billingv1alpha1.Offer
		want        *billingv1alpha1.BillingAccountContract
	}{
		{name: "no entitlement", offer: contractOffer},
		{name: "no contract", entitlement: entitlement("acme", "acme-contract"), offer: contractOffer},
		{name: "offer not published", entitlement: contractEntitlement("acme-contract", startsAt)},
		{
			name:        "fees and usage",
			entitlement: contractEntitlement("acme-contract", startsAt),
			offer:       contractOffer,
			want: &billingv1alpha1.BillingAccountContract{
				StartsAt:          metav1.NewTime(startsAt),
				BillingDayOfMonth: 18,
				Fees: []billingv1alpha1.BillingAccountContractFee{
					{Name: "acme-setup", DisplayName: "Setup fee", ServiceRef: "compute.datumapis.com", ChargeType: billingv1alpha1.ChargeTypeOneTime, Amount: "5000.00", Quantity: 1, Currency: "USD"},
					{Name: "acme-monthly", DisplayName: "Monthly fee", ServiceRef: "compute.datumapis.com", ChargeType: billingv1alpha1.ChargeTypeRecurring, Amount: "2400.00", Quantity: 1, Currency: "USD", Interval: billingv1alpha1.ChargeIntervalMonthly},
				},
				UsageBilled: true,
			},
		},
		{
			name:        "fixed fees only, billed on the 31st",
			entitlement: contractEntitlement("acme-fixed", time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC)),
			offer: publishedOffer("acme-fixed", billingv1alpha1.OfferLaunchStageGA,
				fee("acme-monthly", "Monthly fee", billingv1alpha1.ChargeTypeRecurring, "900"),
			),
			want: &billingv1alpha1.BillingAccountContract{
				StartsAt:          metav1.NewTime(time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC)),
				BillingDayOfMonth: 31,
				Fees: []billingv1alpha1.BillingAccountContractFee{
					{Name: "acme-monthly", DisplayName: "Monthly fee", ServiceRef: "compute.datumapis.com", ChargeType: billingv1alpha1.ChargeTypeRecurring, Amount: "900", Quantity: 1, Currency: "USD", Interval: billingv1alpha1.ChargeIntervalMonthly},
				},
			},
		},
		{
			name:        "fee without a service, with quantity and unit",
			entitlement: contractEntitlement("acme-ports", startsAt),
			offer:       publishedOffer("acme-ports", billingv1alpha1.OfferLaunchStageGA, crossConnects),
			want: &billingv1alpha1.BillingAccountContract{
				StartsAt:          metav1.NewTime(startsAt),
				BillingDayOfMonth: 18,
				Fees: []billingv1alpha1.BillingAccountContractFee{
					{Name: "cross-connect-300", DisplayName: "Cross connect", ChargeType: billingv1alpha1.ChargeTypeRecurring, Amount: "300.00", Quantity: 4, PricingUnit: "port", Currency: "USD", Interval: billingv1alpha1.ChargeIntervalMonthly},
				},
			},
		},
		{
			name:        "fee with periods ends after its last one",
			entitlement: contractEntitlement("acme-term", startsAt),
			offer:       publishedOffer("acme-term", billingv1alpha1.OfferLaunchStageGA, setup, support),
			want: &billingv1alpha1.BillingAccountContract{
				StartsAt:          metav1.NewTime(startsAt),
				BillingDayOfMonth: 18,
				Fees: []billingv1alpha1.BillingAccountContractFee{
					{Name: "acme-setup", DisplayName: "Setup fee", ServiceRef: "compute.datumapis.com", ChargeType: billingv1alpha1.ChargeTypeOneTime, Amount: "5000.00", Quantity: 1, Currency: "USD"},
					{Name: "support-annual", DisplayName: "Premium support", ServiceRef: "compute.datumapis.com", ChargeType: billingv1alpha1.ChargeTypeRecurring, Amount: "1200.00", Quantity: 1, Currency: "USD", Interval: billingv1alpha1.ChargeIntervalAnnual, Periods: 3, EndsAt: &endsAt},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountContract(tt.entitlement, tt.offer); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("accountContract() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReconcileContract(t *testing.T) {
	startsAt := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)

	got := reconcileAccountContract(t,
		contractEntitlement("acme-contract", startsAt),
		publishedOffer("acme-contract", billingv1alpha1.OfferLaunchStageGA,
			fee("acme-monthly", "Monthly fee", billingv1alpha1.ChargeTypeRecurring, "2400.00"),
		),
	)
	if got == nil || !got.StartsAt.Time.Equal(startsAt) || got.BillingDayOfMonth != 18 || len(got.Fees) != 1 {
		t.Errorf("contract = %+v, want the entitlement's contract with its monthly fee", got)
	}

	// Without a contract, a stale one is cleared.
	if got := reconcileAccountContract(t,
		entitlement("acme", "payg"),
		publishedOffer("payg", billingv1alpha1.OfferLaunchStageGA, snapshot("alb-requests", "ALB Requests", -1)),
	); got != nil {
		t.Errorf("contract = %+v, want none", got)
	}
}
