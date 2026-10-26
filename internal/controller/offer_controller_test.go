// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func cpuAllocatedPricing(name, flat string) *billingv1alpha1.ServicePricing {
	return &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: billingv1alpha1.DefaultServicePricingNamespace},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			Metric:      "compute.datumapis.com/instance/cpu-allocated",
			PricingUnit: "vcpu",
			Rates:       []billingv1alpha1.PricingRate{{Flat: flat}},
		},
	}
}

func gaOffer(refs ...string) *billingv1alpha1.Offer {
	offer := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{Name: "compute-pro-v1", Generation: 1},
		Spec: billingv1alpha1.OfferSpec{
			ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
			LaunchStage: billingv1alpha1.OfferLaunchStageGA,
		},
	}
	for _, ref := range refs {
		offer.Spec.ServicePricingRefs = append(offer.Spec.ServicePricingRefs, billingv1alpha1.ServicePricingRef{Name: ref})
	}
	return offer
}

func reconcileOffer(t *testing.T, objs ...client.Object) *billingv1alpha1.Offer {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.Offer{}).
		Build()

	r := &OfferReconciler{client: cl}
	key := types.NamespacedName{Name: "compute-pro-v1"}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got billingv1alpha1.Offer
	if err := cl.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	return &got
}

func TestOfferReconcile_SnapshotsOnePricePerMeter(t *testing.T) {
	got := reconcileOffer(t,
		gaOffer("compute-cpu-allocated-pro-v1"),
		cpuAllocatedPricing("compute-cpu-allocated-v1", "0.025"),
		cpuAllocatedPricing("compute-cpu-allocated-pro-v1", "0.020"),
	)

	if len(got.Spec.ServicePricings) != 1 || got.Spec.ServicePricings[0].Spec.Rates[0].Flat != "0.020" {
		t.Errorf("snapshot = %+v, want the Pro price only", got.Spec.ServicePricings)
	}
}

func TestOfferReconcile_RefusesMeterPricedTwice(t *testing.T) {
	got := reconcileOffer(t,
		gaOffer("compute-cpu-allocated-v1", "compute-cpu-allocated-pro-v1"),
		cpuAllocatedPricing("compute-cpu-allocated-v1", "0.025"),
		cpuAllocatedPricing("compute-cpu-allocated-pro-v1", "0.020"),
	)

	if len(got.Spec.ServicePricings) != 0 {
		t.Errorf("snapshot written for an Offer that prices a meter twice: %+v", got.Spec.ServicePricings)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, ConditionTypePublished)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "MeterPricedTwice" {
		t.Errorf("Published condition = %+v, want False/MeterPricedTwice", cond)
	}
}
