// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestValidatePricingRateXOR(t *testing.T) {
	fldPath := field.NewPath("spec", "rates").Index(0)

	tests := []struct {
		name     string
		rate     billingv1alpha1.PricingRate
		wantErrs int
	}{
		{
			name: "flat only ok",
			rate: billingv1alpha1.PricingRate{
				Flat: "0.05",
			},
			wantErrs: 0,
		},
		{
			name: "tiered only ok",
			rate: billingv1alpha1.PricingRate{
				Tiered: []billingv1alpha1.PricingTierBand{
					{UpTo: "100", Rate: "0.10"},
					{Rate: "0.05"},
				},
			},
			wantErrs: 0,
		},
		{
			name:     "neither flat nor tiered errors",
			rate:     billingv1alpha1.PricingRate{},
			wantErrs: 1,
		},
		{
			name: "both flat and tiered errors",
			rate: billingv1alpha1.PricingRate{
				Flat: "0.05",
				Tiered: []billingv1alpha1.PricingTierBand{
					{Rate: "0.01"},
				},
			},
			wantErrs: 1,
		},
		{
			name: "non-last tier missing upTo errors",
			rate: billingv1alpha1.PricingRate{
				Tiered: []billingv1alpha1.PricingTierBand{
					{Rate: "0.10"},
					{Rate: "0.05"},
				},
			},
			wantErrs: 1,
		},
		{
			name: "last tier may include upTo",
			rate: billingv1alpha1.PricingRate{
				Tiered: []billingv1alpha1.PricingTierBand{
					{UpTo: "100", Rate: "0.10"},
					{UpTo: "200", Rate: "0.05"},
				},
			},
			wantErrs: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := validatePricingRate(&tc.rate, fldPath)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

func TestValidateServicePricingCreate_ChargeTypeShape(t *testing.T) {
	tests := []struct {
		name     string
		spec     billingv1alpha1.ServicePricingSpec
		wantErrs int
	}{
		{
			name: "usage complete ok",
			spec: billingv1alpha1.ServicePricingSpec{
				ChargeType:  billingv1alpha1.ChargeTypeUsage,
				ServiceRef:  "compute.datumapis.com",
				Currency:    "USD",
				Metric:      "compute.datumapis.com/instance/cpu-allocated",
				PricingUnit: "vcpu",
				Rates: []billingv1alpha1.PricingRate{
					{Flat: "0.05"},
				},
			},
			wantErrs: 0,
		},
		{
			name: "usage missing rates",
			spec: billingv1alpha1.ServicePricingSpec{
				ChargeType:  billingv1alpha1.ChargeTypeUsage,
				ServiceRef:  "compute.datumapis.com",
				Currency:    "USD",
				Metric:      "compute.datumapis.com/instance/cpu-allocated",
				PricingUnit: "vcpu",
			},
			wantErrs: 1,
		},
		{
			name: "oneTime complete ok",
			spec: billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeOneTime,
				ServiceRef: "compute.datumapis.com",
				Currency:   "USD",
				Amount:     "10",
				Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
			},
			wantErrs: 0,
		},
		{
			name: "currency not USD",
			spec: billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeOneTime,
				ServiceRef: "compute.datumapis.com",
				Currency:   "EUR",
				Amount:     "10",
				Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
			},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sp := &billingv1alpha1.ServicePricing{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "milo-system"},
				Spec:       tc.spec,
			}
			errs := ValidateServicePricingCreate(sp)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}
