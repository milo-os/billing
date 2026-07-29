// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func newDraftOffer(name string) *billingv1alpha1.Offer {
	return &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				billingv1alpha1.DisplayNameAnnotation: "Test Offer",
			},
		},
		Spec: billingv1alpha1.OfferSpec{
			ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
			LaunchStage: billingv1alpha1.OfferLaunchStageDraft,
			ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
				{Name: "cpu-allocated"},
			},
		},
	}
}

func TestValidateOfferUpdate_GAImmutability(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer
		wantErrs int
	}{
		{
			name: "display-name change ok while GA",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.Annotations[billingv1alpha1.DisplayNameAnnotation] = "Renamed Offer"
				return c
			},
			wantErrs: 0,
		},
		{
			name: "resourceVersion noise ok while GA",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.ResourceVersion = "999"
				c.Generation = 7
				return c
			},
			wantErrs: 0,
		},
		{
			name: "spec chargeTypes change rejected while GA",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.Spec.ChargeTypes = []billingv1alpha1.ChargeType{
					billingv1alpha1.ChargeTypeUsage,
					billingv1alpha1.ChargeTypeOneTime,
				}
				return c
			},
			wantErrs: 1,
		},
		{
			name: "servicePricingRefs change rejected while GA",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.Spec.ServicePricingRefs = append(c.Spec.ServicePricingRefs,
					billingv1alpha1.ServicePricingRef{Name: "other"})
				return c
			},
			wantErrs: 1,
		},
		{
			name: "GA to Draft rejected",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageDraft
				return c
			},
			wantErrs: 1,
		},
		{
			name: "other annotation change rejected while GA",
			mutate: func(old *billingv1alpha1.Offer) *billingv1alpha1.Offer {
				c := old.DeepCopy()
				c.Annotations["custom.io/note"] = "nope"
				return c
			},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := newDraftOffer("pro-v1")
			old.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
			old.Spec.ServicePricings = []billingv1alpha1.ServicePricingSnapshot{
				{
					Name: "cpu-allocated",
					Spec: billingv1alpha1.ServicePricingSpec{
						ChargeType:  billingv1alpha1.ChargeTypeUsage,
						ServiceRef:  "compute.datumapis.com",
						Currency:    "USD",
						Metric:      "compute.datumapis.com/instance/cpu-allocated",
						PricingUnit: "vcpu",
						Rates:       []billingv1alpha1.PricingRate{{Flat: "0.05"}},
					},
				},
			}
			newOffer := tc.mutate(old)
			errs := ValidateOfferUpdate(old, newOffer)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

func TestValidateOfferUpdate_PublishTransitionsAllowed(t *testing.T) {
	t.Run("draft to GA ok", func(t *testing.T) {
		old := newDraftOffer("pro-v1")
		newOffer := old.DeepCopy()
		newOffer.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
		errs := ValidateOfferUpdate(old, newOffer)
		if len(errs) != 0 {
			t.Errorf("expected no errors for Draft→GA, got: %v", errs)
		}
	})

	t.Run("controller snapshot fill ok", func(t *testing.T) {
		old := newDraftOffer("pro-v1")
		old.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
		newOffer := old.DeepCopy()
		newOffer.Spec.ServicePricings = []billingv1alpha1.ServicePricingSnapshot{
			{
				Name: "cpu-allocated",
				Spec: billingv1alpha1.ServicePricingSpec{
					ChargeType:  billingv1alpha1.ChargeTypeUsage,
					ServiceRef:  "compute.datumapis.com",
					Currency:    "USD",
					Metric:      "compute.datumapis.com/instance/cpu-allocated",
					PricingUnit: "vcpu",
					Rates:       []billingv1alpha1.PricingRate{{Flat: "0.05"}},
				},
			},
		}
		errs := ValidateOfferUpdate(old, newOffer)
		if len(errs) != 0 {
			t.Errorf("expected no errors for snapshot fill, got: %v", errs)
		}
	})
}

func TestValidateOfferCreate_ChargeTypesCoverSnapshot(t *testing.T) {
	offer := newDraftOffer("pro-v1")
	offer.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
	offer.Spec.ServicePricings = []billingv1alpha1.ServicePricingSnapshot{
		{
			Name: "setup-fee",
			Spec: billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeOneTime,
				ServiceRef: "compute.datumapis.com",
				Currency:   "USD",
				Amount:     "10",
				Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
			},
		},
	}

	errs := ValidateOfferCreate(offer)
	if len(errs) == 0 {
		t.Fatal("expected error when chargeTypes missing OneTime from snapshot")
	}
}
