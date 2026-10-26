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

func TestOfferReconciler_BuildSnapshots_Discounts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	cpuPricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "compute-cpu",
			Namespace: billingv1alpha1.DefaultServicePricingNamespace,
		},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			Metric:      "compute.datumapis.com/instance/cpu-allocated",
			PricingUnit: "vcpu",
			Rates:       []billingv1alpha1.PricingRate{{Flat: "0.05"}},
		},
	}

	memPricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "compute-mem",
			Namespace: billingv1alpha1.DefaultServicePricingNamespace,
		},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			Metric:      "compute.datumapis.com/instance/memory-allocated",
			PricingUnit: "gib",
			Rates:       []billingv1alpha1.PricingRate{{Flat: "0.01"}},
		},
	}

	setupFeePricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "setup-fee",
			Namespace: billingv1alpha1.DefaultServicePricingNamespace,
		},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType: billingv1alpha1.ChargeTypeOneTime,
			ServiceRef: "compute.datumapis.com",
			Currency:   "USD",
			Amount:     "10.00",
			Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cpuPricing, memPricing, setupFeePricing).
		Build()

	r := &OfferReconciler{client: cl}

	t.Run("inherits offer discount across all services (usage and one-time)", func(t *testing.T) {
		offer := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{Name: "payg-promo"},
			Spec: billingv1alpha1.OfferSpec{
				ChargeTypes: []billingv1alpha1.ChargeType{
					billingv1alpha1.ChargeTypeUsage,
					billingv1alpha1.ChargeTypeOneTime,
				},
				LaunchStage: billingv1alpha1.OfferLaunchStageGA,
				Discount:    &billingv1alpha1.Discount{Percentage: 20},
				ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{Name: "compute-cpu"},
					{Name: "setup-fee"},
				},
			},
		}

		snapshots, err := r.buildSnapshots(context.Background(), offer)
		if err != nil {
			t.Fatalf("buildSnapshots: %v", err)
		}
		if len(snapshots) != 2 {
			t.Fatalf("snapshots len = %d, want 2", len(snapshots))
		}

		// compute-cpu should inherit offer discount (20%)
		if snapshots[0].Discount == nil || snapshots[0].Discount.Percentage != 20 {
			t.Errorf("snapshots[0].Discount = %v, want 20", snapshots[0].Discount)
		}

		// setup-fee (OneTime) should also inherit offer discount (20%)
		if snapshots[1].Discount == nil || snapshots[1].Discount.Percentage != 20 {
			t.Errorf("snapshots[1].Discount = %v, want 20", snapshots[1].Discount)
		}
	})

	t.Run("service ref discount overrides offer discount", func(t *testing.T) {
		offer := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{Name: "payg-override"},
			Spec: billingv1alpha1.OfferSpec{
				ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
				LaunchStage: billingv1alpha1.OfferLaunchStageGA,
				Discount:    &billingv1alpha1.Discount{Percentage: 20},
				ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{
						Name:     "compute-cpu",
						Discount: &billingv1alpha1.Discount{Percentage: 100},
					},
					{Name: "compute-mem"},
				},
			},
		}

		snapshots, err := r.buildSnapshots(context.Background(), offer)
		if err != nil {
			t.Fatalf("buildSnapshots: %v", err)
		}
		if len(snapshots) != 2 {
			t.Fatalf("snapshots len = %d, want 2", len(snapshots))
		}

		// cpu has override: 100%
		if snapshots[0].Discount == nil || snapshots[0].Discount.Percentage != 100 {
			t.Errorf("snapshots[0].Discount = %v, want 100", snapshots[0].Discount)
		}

		// mem inherits offer default: 20%
		if snapshots[1].Discount == nil || snapshots[1].Discount.Percentage != 20 {
			t.Errorf("snapshots[1].Discount = %v, want 20", snapshots[1].Discount)
		}
	})

	t.Run("no discount produces nil discount in snapshots", func(t *testing.T) {
		offer := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{Name: "payg-no-disc"},
			Spec: billingv1alpha1.OfferSpec{
				ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
				LaunchStage: billingv1alpha1.OfferLaunchStageGA,
				ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{Name: "compute-cpu"},
				},
			},
		}

		snapshots, err := r.buildSnapshots(context.Background(), offer)
		if err != nil {
			t.Fatalf("buildSnapshots: %v", err)
		}
		if len(snapshots) != 1 {
			t.Fatalf("snapshots len = %d, want 1", len(snapshots))
		}
		if snapshots[0].Discount != nil {
			t.Errorf("snapshots[0].Discount = %v, want nil", snapshots[0].Discount)
		}
	})

	t.Run("explicit 0 percent discount override is preserved", func(t *testing.T) {
		offer := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{Name: "payg-zero-override"},
			Spec: billingv1alpha1.OfferSpec{
				ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
				LaunchStage: billingv1alpha1.OfferLaunchStageGA,
				Discount:    &billingv1alpha1.Discount{Percentage: 30},
				ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{
						Name:     "compute-cpu",
						Discount: &billingv1alpha1.Discount{Percentage: 0},
					},
				},
			},
		}

		snapshots, err := r.buildSnapshots(context.Background(), offer)
		if err != nil {
			t.Fatalf("buildSnapshots: %v", err)
		}
		if len(snapshots) != 1 {
			t.Fatalf("snapshots len = %d, want 1", len(snapshots))
		}
		if snapshots[0].Discount == nil || snapshots[0].Discount.Percentage != 0 {
			t.Errorf("snapshots[0].Discount = %v, want 0", snapshots[0].Discount)
		}
	})
}

func TestOfferReconciler_ReconcileGA_SnapshotsDiscounts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	cpuPricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "compute-cpu",
			Namespace: billingv1alpha1.DefaultServicePricingNamespace,
		},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			Metric:      "compute.datumapis.com/instance/cpu-allocated",
			PricingUnit: "vcpu",
			Rates:       []billingv1alpha1.PricingRate{{Flat: "0.05"}},
		},
	}

	offer := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{Name: "payg-ga"},
		Spec: billingv1alpha1.OfferSpec{
			ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
			LaunchStage: billingv1alpha1.OfferLaunchStageGA,
			Discount:    &billingv1alpha1.Discount{Percentage: 50},
			ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
				{Name: "compute-cpu"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&billingv1alpha1.Offer{}).
		WithObjects(cpuPricing, offer).
		Build()

	r := &OfferReconciler{client: cl}

	// First reconcile should write snapshot
	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "payg-ga"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatal("unexpected requeue")
	}

	var updated billingv1alpha1.Offer
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "payg-ga"}, &updated); err != nil {
		t.Fatalf("Get updated offer: %v", err)
	}

	if len(updated.Spec.ServicePricings) != 1 {
		t.Fatalf("servicePricings len = %d, want 1", len(updated.Spec.ServicePricings))
	}
	snap := updated.Spec.ServicePricings[0]
	if snap.Discount == nil || snap.Discount.Percentage != 50 {
		t.Fatalf("snapshot discount = %v, want 50", snap.Discount)
	}

	// Second reconcile should update conditions to Published
	res, err = r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "payg-ga"},
	})
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	if err := cl.Get(context.Background(), types.NamespacedName{Name: "payg-ga"}, &updated); err != nil {
		t.Fatalf("Get updated offer: %v", err)
	}
	readyFound := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == ConditionTypeReady && cond.Status == metav1.ConditionTrue {
			readyFound = true
			break
		}
	}
	if !readyFound {
		t.Errorf("Ready condition not found or not True: %v", updated.Status.Conditions)
	}
}

func TestOfferReconciler_MissingServicePricing_SetsCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	offer := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{Name: "payg-missing"},
		Spec: billingv1alpha1.OfferSpec{
			ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
			LaunchStage: billingv1alpha1.OfferLaunchStageGA,
			ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
				{Name: "nonexistent-pricing"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&billingv1alpha1.Offer{}).
		WithObjects(offer).
		Build()

	r := &OfferReconciler{client: cl}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "payg-missing"},
	})
	if err != nil {
		t.Fatalf("Reconcile should not return error on business failure, got: %v", err)
	}

	var updated billingv1alpha1.Offer
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "payg-missing"}, &updated); err != nil {
		t.Fatalf("Get updated offer: %v", err)
	}

	if len(updated.Spec.ServicePricings) != 0 {
		t.Fatalf("servicePricings snapshot should not be written on error")
	}

	failedReasonFound := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == ConditionTypeReady && cond.Reason == "ServicePricingResolveFailed" {
			failedReasonFound = true
			break
		}
	}
	if !failedReasonFound {
		t.Errorf("ServicePricingResolveFailed condition not found: %v", updated.Status.Conditions)
	}
}
