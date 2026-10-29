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

// snapshot is one entry of a published Offer's servicePricings, with a nil
// discount when percentage is negative.
func snapshot(name, displayName string, percentage int32) billingv1alpha1.ServicePricingSnapshot {
	s := billingv1alpha1.ServicePricingSnapshot{
		Name: name,
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "networking.datumapis.com",
			Currency:    "USD",
			DisplayName: displayName,
		},
	}
	if percentage >= 0 {
		s.Discount = &billingv1alpha1.Discount{Percentage: percentage}
	}
	return s
}

func publishedOffer(name string, stage billingv1alpha1.OfferLaunchStage, pricings ...billingv1alpha1.ServicePricingSnapshot) *billingv1alpha1.Offer {
	return &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: billingv1alpha1.OfferSpec{
			ChargeTypes:     []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
			LaunchStage:     stage,
			ServicePricings: pricings,
		},
	}
}

func entitlement(name, offer string) *billingv1alpha1.BillingEntitlement {
	return &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme"},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			OfferRef:          billingv1alpha1.OfferReference{Name: offer},
		},
	}
}

func reconcileAccountDiscounts(t *testing.T, objs ...client.Object) []billingv1alpha1.BillingAccountDiscount {
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
			Discounts: []billingv1alpha1.BillingAccountDiscount{{Name: "stale", Percentage: 50}},
		},
	}
	r := &BillingAccountReconciler{client: cl}
	if err := r.reconcileDiscounts(context.Background(), account); err != nil {
		t.Fatalf("reconcileDiscounts: %v", err)
	}
	return account.Status.Discounts
}

func TestAccountDiscounts(t *testing.T) {
	tests := []struct {
		name  string
		offer *billingv1alpha1.Offer
		want  []billingv1alpha1.BillingAccountDiscount
	}{
		{name: "no offer", offer: nil, want: nil},
		{
			name: "no discount above 0%",
			offer: publishedOffer("payg", billingv1alpha1.OfferLaunchStageGA,
				snapshot("alb-requests", "ALB Requests", -1),
				snapshot("alb-egress", "ALB Egress", 0),
			),
			want: nil,
		},
		{
			name: "lists every pricing once one is discounted",
			offer: publishedOffer("payg-acme", billingv1alpha1.OfferLaunchStageGA,
				snapshot("alb-requests", "ALB Requests", 20),
				snapshot("alb-egress", "ALB Egress", -1),
			),
			want: []billingv1alpha1.BillingAccountDiscount{
				{Name: "alb-requests", ServiceRef: "networking.datumapis.com", DisplayName: "ALB Requests", Percentage: 20},
				{Name: "alb-egress", ServiceRef: "networking.datumapis.com", DisplayName: "ALB Egress", Percentage: 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountDiscounts(tt.offer); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("accountDiscounts() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReconcileDiscounts_PublishesEntitledOffer(t *testing.T) {
	got := reconcileAccountDiscounts(t,
		entitlement("acme", "payg-acme"),
		publishedOffer("payg-acme", billingv1alpha1.OfferLaunchStageGA,
			snapshot("alb-requests", "ALB Requests", 100),
		),
	)
	want := []billingv1alpha1.BillingAccountDiscount{
		{Name: "alb-requests", ServiceRef: "networking.datumapis.com", DisplayName: "ALB Requests", Percentage: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discounts = %+v, want %+v", got, want)
	}
}

func TestReconcileDiscounts_ClearsWithoutPublishedOffer(t *testing.T) {
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
		{
			name: "GA offer without a snapshot yet",
			objs: []client.Object{
				entitlement("acme", "pending"),
				publishedOffer("pending", billingv1alpha1.OfferLaunchStageGA),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reconcileAccountDiscounts(t, tt.objs...); got != nil {
				t.Errorf("discounts = %+v, want none", got)
			}
		})
	}
}
