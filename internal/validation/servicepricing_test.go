// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			errs := validateServicePricingSpec(&sp.Spec, field.NewPath("spec"))
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

func TestValidatePricingRateMatches(t *testing.T) {
	match := func(dim, value string) *billingv1alpha1.DimensionMatch {
		return &billingv1alpha1.DimensionMatch{Dimension: dim, Value: value}
	}

	tests := []struct {
		name     string
		rates    []billingv1alpha1.PricingRate
		wantErrs int
	}{
		{
			name: "matched values and one catch-all ok",
			rates: []billingv1alpha1.PricingRate{
				{Match: match("tier", "standard"), Flat: "0.025"},
				{Match: match("tier", "premium"), Flat: "0.04"},
				{Flat: "0.03"},
			},
		},
		{
			name: "two dimensions",
			rates: []billingv1alpha1.PricingRate{
				{Match: match("tier", "standard"), Flat: "0.025"},
				{Match: match("region", "us-central1"), Flat: "0.04"},
			},
			wantErrs: 1,
		},
		{
			name: "same value twice",
			rates: []billingv1alpha1.PricingRate{
				{Match: match("tier", "standard"), Flat: "0.025"},
				{Match: match("tier", "standard"), Flat: "0.04"},
			},
			wantErrs: 1,
		},
		{
			name: "two catch-alls",
			rates: []billingv1alpha1.PricingRate{
				{Flat: "0.025"},
				{Flat: "0.04"},
			},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := validatePricingRateMatches(tc.rates, field.NewPath("spec", "rates"))
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

const testMeterName = "compute.datumapis.com/instance/cpu-allocated"

func testMeter(phase billingv1alpha1.Phase, service string) *billingv1alpha1.MeterDefinition {
	md := &billingv1alpha1.MeterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "compute-datumapis-com-instance-cpu-allocated"},
		Spec: billingv1alpha1.MeterDefinitionSpec{
			MeterName:   testMeterName,
			Phase:       phase,
			DisplayName: "Allocated vCPU",
			Measurement: billingv1alpha1.MeterMeasurement{
				Aggregation: billingv1alpha1.MeterAggregationAverage,
				Unit:        "{vcpu}",
				Dimensions:  []string{"tier", "region"},
			},
			MonitoredResourceTypes: []string{"compute.datumapis.com/Instance"},
		},
	}
	if service != "" {
		md.Labels = map[string]string{billingv1alpha1.MeterServiceLabel: service}
	}
	return md
}

func testService(serviceName string) *unstructured.Unstructured {
	svc := &unstructured.Unstructured{}
	svc.SetGroupVersionKind(serviceListGVK.GroupVersion().WithKind("Service"))
	svc.SetName(strings.ReplaceAll(serviceName, ".", "-"))
	_ = unstructured.SetNestedField(svc.Object, serviceName, "spec", "serviceName")
	return svc
}

// newPricingClient returns a fake client that also serves the
// services.miloapis.com Service kind, which billing reads unstructured.
func newPricingClient(objs ...client.Object) client.Client {
	scheme := newScheme()
	gv := serviceListGVK.GroupVersion()
	scheme.AddKnownTypeWithName(gv.WithKind("Service"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("ServiceList"), &unstructured.UnstructuredList{})
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func usagePricing(serviceRef string, rates ...billingv1alpha1.PricingRate) *billingv1alpha1.ServicePricing {
	return &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "compute-cpu-allocated-v1", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  serviceRef,
			Currency:    "USD",
			Metric:      testMeterName,
			PricingUnit: "vcpu",
			Rates:       rates,
		},
	}
}

func TestValidateServicePricingCreate_Meter(t *testing.T) {
	const compute = "compute.datumapis.com"
	tierRate := billingv1alpha1.PricingRate{
		Match: &billingv1alpha1.DimensionMatch{Dimension: "tier", Value: "standard"},
		Flat:  "0.025",
	}

	tests := []struct {
		name     string
		meter    *billingv1alpha1.MeterDefinition
		pricing  *billingv1alpha1.ServicePricing
		wantErrs int
	}{
		{
			name:    "published meter, declared dimension, owning service",
			meter:   testMeter(billingv1alpha1.PhasePublished, compute),
			pricing: usagePricing(compute, tierRate, billingv1alpha1.PricingRate{Flat: "0.03"}),
		},
		{
			name:  "project_name is always matchable",
			meter: testMeter(billingv1alpha1.PhasePublished, compute),
			pricing: usagePricing(compute, billingv1alpha1.PricingRate{
				Match: &billingv1alpha1.DimensionMatch{Dimension: billingv1alpha1.SystemDimensionProjectName, Value: "p"},
				Flat:  "0",
			}),
		},
		{
			name:     "meter doesn't exist",
			pricing:  usagePricing(compute, tierRate),
			wantErrs: 1,
		},
		{
			name:     "meter retired",
			meter:    testMeter(billingv1alpha1.PhaseRetired, compute),
			pricing:  usagePricing(compute, tierRate),
			wantErrs: 1,
		},
		{
			name:  "undeclared dimension",
			meter: testMeter(billingv1alpha1.PhasePublished, compute),
			pricing: usagePricing(compute, billingv1alpha1.PricingRate{
				Match: &billingv1alpha1.DimensionMatch{Dimension: "model", Value: "x"},
				Flat:  "0.01",
			}),
			wantErrs: 1,
		},
		{
			name:     "serviceRef isn't the meter's service",
			meter:    testMeter(billingv1alpha1.PhasePublished, compute),
			pricing:  usagePricing("networking.datumapis.com", tierRate),
			wantErrs: 1,
		},
		{
			name:     "meter has no service label",
			meter:    testMeter(billingv1alpha1.PhasePublished, ""),
			pricing:  usagePricing(compute, tierRate),
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.meter != nil {
				objs = append(objs, tc.meter)
			}
			errs := ValidateServicePricingCreate(context.Background(), newPricingClient(objs...), tc.pricing)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

func TestValidateServicePricingCreate_Service(t *testing.T) {
	fee := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "assistant-access-fee-v1", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType: billingv1alpha1.ChargeTypeRecurring,
			ServiceRef: "assistant.miloapis.com",
			Currency:   "USD",
			Amount:     "10.00",
			Interval:   billingv1alpha1.ChargeIntervalMonthly,
		},
	}

	t.Run("service exists", func(t *testing.T) {
		cl := newPricingClient(testService("assistant.miloapis.com"))
		if errs := ValidateServicePricingCreate(context.Background(), cl, fee); len(errs) != 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("service missing", func(t *testing.T) {
		cl := newPricingClient(testService("compute.datumapis.com"))
		if errs := ValidateServicePricingCreate(context.Background(), cl, fee); len(errs) != 1 {
			t.Errorf("got %d errors, want 1: %v", len(errs), errs)
		}
	})
}

func TestValidateServicePricingUpdate(t *testing.T) {
	old := usagePricing("compute.datumapis.com", billingv1alpha1.PricingRate{Flat: "0.025"})

	t.Run("metadata change ok", func(t *testing.T) {
		updated := old.DeepCopy()
		updated.Finalizers = []string{"billing.miloapis.com/provider"}
		if errs := ValidateServicePricingUpdate(old, updated); len(errs) != 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("rate change rejected", func(t *testing.T) {
		updated := old.DeepCopy()
		updated.Spec.Rates[0].Flat = "0.030"
		if errs := ValidateServicePricingUpdate(old, updated); len(errs) != 1 {
			t.Errorf("got %d errors, want 1: %v", len(errs), errs)
		}
	})
}
