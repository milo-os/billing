// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func entitlementTestClient(t *testing.T) client.Client {
	t.Helper()
	offer := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{Name: "bare-metal-acme"},
		Spec: billingv1alpha1.OfferSpec{
			LaunchStage:     billingv1alpha1.OfferLaunchStageGA,
			ServicePricings: []billingv1alpha1.ServicePricingSnapshot{{Name: "setup-fee"}},
		},
	}
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(offer).Build()
}

func contractEntitlement(startsAt *time.Time) *billingv1alpha1.BillingEntitlement {
	be := &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "be-acme"},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			OfferRef:          billingv1alpha1.OfferReference{Name: "bare-metal-acme"},
		},
	}
	if startsAt != nil {
		be.Spec.Contract = &billingv1alpha1.BillingContract{StartsAt: metav1.NewTime(*startsAt)}
	}
	return be
}

func TestValidateBillingEntitlementCreate_Contract(t *testing.T) {
	now := time.Date(2026, time.March, 20, 15, 4, 5, 0, time.UTC)
	day := func(month time.Month, d int) *time.Time {
		t := time.Date(2026, month, d, 0, 0, 0, 0, time.UTC)
		return &t
	}

	tests := []struct {
		name     string
		startsAt *time.Time
		wantErr  bool
	}{
		{name: "no contract"},
		{name: "starts today", startsAt: day(time.March, 20)},
		{name: "starts next month", startsAt: day(time.April, 18)},
		{name: "starts on the 31st", startsAt: day(time.March, 31)},
		{name: "a year ahead", startsAt: func() *time.Time { t := time.Date(2027, time.March, 20, 0, 0, 0, 0, time.UTC); return &t }()},
		{name: "started yesterday", startsAt: day(time.March, 19), wantErr: true},
		{name: "backdated a week", startsAt: day(time.March, 13), wantErr: true},
		{name: "more than a year ahead", startsAt: func() *time.Time { t := time.Date(2027, time.March, 22, 0, 0, 0, 0, time.UTC); return &t }(), wantErr: true},
		{name: "not midnight UTC", startsAt: func() *time.Time { t := time.Date(2026, time.March, 18, 9, 0, 0, 0, time.UTC); return &t }(), wantErr: true},
		{name: "midnight in another zone", startsAt: func() *time.Time {
			t := time.Date(2026, time.March, 18, 0, 0, 0, 0, time.FixedZone("EST", -5*60*60))
			return &t
		}(), wantErr: true},
		{name: "zero start", startsAt: &time.Time{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateBillingEntitlementCreate(context.Background(), entitlementTestClient(t), contractEntitlement(tt.startsAt), now)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateBillingEntitlementCreate() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateBillingEntitlementUpdate_Contract(t *testing.T) {
	now := time.Date(2026, time.March, 18, 15, 4, 5, 0, time.UTC)
	march18 := time.Date(2026, time.March, 18, 0, 0, 0, 0, time.UTC)
	march19 := march18.AddDate(0, 0, 1)
	march17 := march18.AddDate(0, 0, -1)
	march19Noon := march19.Add(12 * time.Hour)

	tests := []struct {
		name        string
		old, new    *time.Time
		changeOffer bool
		// oneTime makes the new Offer charge a one-time fee.
		oneTime bool
		wantErr bool
	}{
		{name: "no contract either side"},
		{name: "contract unchanged", old: &march18, new: &march18},
		{name: "offer changed mid-term", old: &march18, new: &march18, changeOffer: true},
		{name: "contract added, starting today", new: &march18},
		{name: "contract added with a new offer", new: &march19, changeOffer: true},
		{name: "contract added, started yesterday", new: &march17, wantErr: true},
		{name: "contract added, not midnight UTC", new: &march19Noon, wantErr: true},
		{name: "contract removed", old: &march18, wantErr: true},
		{name: "start moved", old: &march18, new: &march19, wantErr: true},
		// A contract that has started stays valid once its day has passed.
		{name: "started contract unchanged", old: &march17, new: &march17},
		// An amendment would charge the one-time fees again.
		{name: "started contract amended with a one-time fee", old: &march17, new: &march17, changeOffer: true, oneTime: true, wantErr: true},
		{name: "contract starting today amended with a one-time fee", old: &march18, new: &march18, changeOffer: true, oneTime: true, wantErr: true},
		{name: "contract not started yet changed to a one-time fee", old: &march19, new: &march19, changeOffer: true, oneTime: true},
		{name: "contract added with a one-time fee", new: &march19, changeOffer: true, oneTime: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := entitlementTestClient(t)
			if tt.changeOffer {
				other := &billingv1alpha1.Offer{
					ObjectMeta: metav1.ObjectMeta{Name: "bare-metal-acme-v2"},
					Spec: billingv1alpha1.OfferSpec{
						LaunchStage: billingv1alpha1.OfferLaunchStageGA,
						ServicePricings: []billingv1alpha1.ServicePricingSnapshot{{Name: "monthly-fee",
							Spec: billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeRecurring}}},
					},
				}
				if tt.oneTime {
					other.Spec.ServicePricings = append(other.Spec.ServicePricings, billingv1alpha1.ServicePricingSnapshot{
						Name: "setup-fee", Spec: billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeOneTime}})
				}
				if err := c.Create(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			newBE := contractEntitlement(tt.new)
			if tt.changeOffer {
				newBE.Spec.OfferRef.Name = "bare-metal-acme-v2"
			}
			errs := ValidateBillingEntitlementUpdate(context.Background(), c, contractEntitlement(tt.old), newBE, now)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateBillingEntitlementUpdate() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}
