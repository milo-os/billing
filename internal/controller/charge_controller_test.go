// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestChargeReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-connect-install-v1", Namespace: "milo-system"},
		Spec:       billingv1alpha1.ServicePricingSpec{ChargeType: billingv1alpha1.ChargeTypeOneTime, Amount: "300"},
	}
	charge := func(name, unit string) *billingv1alpha1.Charge {
		return &billingv1alpha1.Charge{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme", Generation: 1},
			Spec: billingv1alpha1.ChargeSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: pricing.Name, Namespace: "milo-system"},
				Description:       "Cross-connect install",
				Quantity:          "2",
				UnitAmount:        unit,
				DiscountPercent:   "10",
			},
		}
	}
	scheduled := charge("scheduled", "300.00")
	scheduled.Status.Phase = billingv1alpha1.ChargePhaseScheduled

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(pricing, charge("list-price", "300.00"), charge("changed-price", "250.00"), scheduled).
		WithStatusSubresource(&billingv1alpha1.Charge{}).Build()
	r := &ChargeReconciler{client: c}

	tests := []struct {
		name                    string
		phase                   billingv1alpha1.ChargePhase
		amount, due, listAmount string
	}{
		{name: "list-price", phase: billingv1alpha1.ChargePhasePending, amount: "600.00", due: "540.00"},
		{name: "changed-price", phase: billingv1alpha1.ChargePhasePending, amount: "500.00", due: "450.00", listAmount: "300.00"},
		// The provider's phase is never overwritten.
		{name: "scheduled", phase: billingv1alpha1.ChargePhaseScheduled, amount: "600.00", due: "540.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := types.NamespacedName{Namespace: "org-acme", Name: tt.name}
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			var got billingv1alpha1.Charge
			if err := c.Get(context.Background(), key, &got); err != nil {
				t.Fatal(err)
			}
			s := got.Status
			if s.Phase != tt.phase || s.Amount != tt.amount || s.AmountDue != tt.due || s.ListUnitAmount != tt.listAmount {
				t.Fatalf("got phase %s, amount %s, due %s, list %q", s.Phase, s.Amount, s.AmountDue, s.ListUnitAmount)
			}
			if s.ObservedGeneration != 1 {
				t.Fatalf("observedGeneration = %d", s.ObservedGeneration)
			}
		})
	}
}
