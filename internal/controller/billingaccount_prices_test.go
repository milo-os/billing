// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func reconcileAccountPrices(t *testing.T, objs ...client.Object) []billingv1alpha1.BillingAccountPrice {
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
			Prices: []billingv1alpha1.BillingAccountPrice{{Name: "stale", ChargeType: billingv1alpha1.ChargeTypeUsage}},
		},
	}
	r := &BillingAccountReconciler{client: cl}
	if err := r.reconcileEntitlementSummary(context.Background(), account); err != nil {
		t.Fatalf("reconcileEntitlementSummary: %v", err)
	}
	return account.Status.Prices
}

// usagePricing is a Usage entry of a published Offer's servicePricings, with
// a flat default rate, a tiered one and one matched to a dimension value.
func usagePricing() billingv1alpha1.ServicePricingSnapshot {
	s := snapshot("alb-egress", "ALB Egress", -1)
	s.Spec.SKU = "NET-ALB-EG"
	s.Spec.Metric = "networking.datumapis.com/alb/egress-bytes"
	s.Spec.PricingUnit = "gib"
	s.Spec.UnitConversion = &billingv1alpha1.UnitConversion{MeterUnitsPerPricingUnit: "1073741824"}
	s.Spec.Tax = &billingv1alpha1.PricingTax{Code: "txcd_10103001"}
	s.Spec.BasedOn = "alb-egress-list"
	s.Spec.Rates = []billingv1alpha1.PricingRate{
		{
			Match: &billingv1alpha1.DimensionMatch{Dimension: "region", Value: "eu"},
			Flat:  "0.09",
		},
		{
			Match: &billingv1alpha1.DimensionMatch{Dimension: "region", Value: "us"},
			Tiered: []billingv1alpha1.PricingTierBand{
				{UpTo: "100", Rate: "0"},
				{Rate: "0.05"},
			},
		},
		{Flat: "0.08"},
	}
	return s
}

func TestAccountPrices(t *testing.T) {
	usage := usagePricing()

	monthly := fee("cross-connect-300", "Cross connect", billingv1alpha1.ChargeTypeRecurring, "300.00")
	monthly.Spec.ServiceRef = ""
	monthly.Spec.PricingUnit = "port"
	monthly.Spec.SKU = "DC-XC-01"
	monthly.Quantity = 4
	monthly.Periods = 12
	monthly.BillingTiming = billingv1alpha1.BillingTimingInArrears
	monthly.Discount = &billingv1alpha1.Discount{Percentage: 10}

	setup := fee("acme-setup", "Setup fee", billingv1alpha1.ChargeTypeOneTime, "5000.00")
	setup.Discount = &billingv1alpha1.Discount{Percentage: 0}

	tests := []struct {
		name  string
		offer *billingv1alpha1.Offer
		want  []billingv1alpha1.BillingAccountPrice
	}{
		{name: "no offer", offer: nil, want: nil},
		{
			name:  "GA offer without a snapshot yet",
			offer: publishedOffer("pending", billingv1alpha1.OfferLaunchStageGA),
			want:  nil,
		},
		{
			name:  "lists usage rates and fixed fees in snapshot order",
			offer: publishedOffer("acme-contract", billingv1alpha1.OfferLaunchStageGA, usage, monthly, setup),
			want: []billingv1alpha1.BillingAccountPrice{
				{
					Name:        "alb-egress",
					ServiceRef:  "networking.datumapis.com",
					DisplayName: "ALB Egress",
					SKU:         "NET-ALB-EG",
					ChargeType:  billingv1alpha1.ChargeTypeUsage,
					Currency:    "USD",
					Metric:      "networking.datumapis.com/alb/egress-bytes",
					PricingUnit: "gib",
					Rates:       usage.Spec.Rates,
				},
				{
					Name:               "cross-connect-300",
					DisplayName:        "Cross connect",
					SKU:                "DC-XC-01",
					ChargeType:         billingv1alpha1.ChargeTypeRecurring,
					Currency:           "USD",
					PricingUnit:        "port",
					Amount:             "300.00",
					Quantity:           4,
					Interval:           billingv1alpha1.ChargeIntervalMonthly,
					DiscountPercentage: 10,
				},
				{
					Name:        "acme-setup",
					ServiceRef:  "compute.datumapis.com",
					DisplayName: "Setup fee",
					ChargeType:  billingv1alpha1.ChargeTypeOneTime,
					Currency:    "USD",
					Amount:      "5000.00",
					Quantity:    1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountPrices(tt.offer); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("accountPrices() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The account's rates are a copy, so nothing that changes them changes the
// Offer they came from.
func TestAccountPrices_CopiesRates(t *testing.T) {
	offer := publishedOffer("payg", billingv1alpha1.OfferLaunchStageGA, usagePricing())
	got := accountPrices(offer)
	got[0].Rates[1].Tiered[0].Rate = "1"
	if rate := offer.Spec.ServicePricings[0].Spec.Rates[1].Tiered[0].Rate; rate != "0" {
		t.Errorf("offer's rate = %q after changing the account's, want it unchanged", rate)
	}
}

func TestReconcilePrices(t *testing.T) {
	discounted := snapshot("alb-requests", "ALB Requests", 20)
	discounted.Spec.Metric = "networking.datumapis.com/alb/requests"
	discounted.Spec.PricingUnit = "request"
	discounted.Spec.Rates = []billingv1alpha1.PricingRate{{Flat: "0.0000004"}}

	got := reconcileAccountPrices(t,
		entitlement("acme", "payg-acme"),
		publishedOffer("payg-acme", billingv1alpha1.OfferLaunchStageGA, discounted),
	)
	want := []billingv1alpha1.BillingAccountPrice{{
		Name:               "alb-requests",
		ServiceRef:         "networking.datumapis.com",
		DisplayName:        "ALB Requests",
		ChargeType:         billingv1alpha1.ChargeTypeUsage,
		Currency:           "USD",
		Metric:             "networking.datumapis.com/alb/requests",
		PricingUnit:        "request",
		Rates:              []billingv1alpha1.PricingRate{{Flat: "0.0000004"}},
		DiscountPercentage: 20,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prices = %+v, want %+v", got, want)
	}
}

func TestReconcilePrices_ClearsWithoutPublishedOffer(t *testing.T) {
	tests := []struct {
		name string
		objs []client.Object
	}{
		{name: "no entitlement"},
		{name: "offer missing", objs: []client.Object{entitlement("acme", "gone")}},
		{
			name: "offer not GA",
			objs: []client.Object{
				entitlement("acme", "draft"),
				publishedOffer("draft", billingv1alpha1.OfferLaunchStageDraft),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reconcileAccountPrices(t, tt.objs...); got != nil {
				t.Errorf("prices = %+v, want none", got)
			}
		})
	}
}
