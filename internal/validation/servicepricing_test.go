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

func TestValidateServicePricingCreate_FeeWithoutService(t *testing.T) {
	fee := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-connect-300", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeRecurring,
			Currency:    "USD",
			DisplayName: "Cross connect",
			Amount:      "300.00",
			Interval:    billingv1alpha1.ChargeIntervalMonthly,
			PricingUnit: "port",
		},
	}
	if errs := ValidateServicePricingCreate(context.Background(), newPricingClient(), fee); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}

	usage := usagePricing("", billingv1alpha1.PricingRate{Flat: "0.03"})
	cl := newPricingClient(testMeter(billingv1alpha1.PhasePublished, "compute.datumapis.com"))
	errs := ValidateServicePricingCreate(context.Background(), cl, usage)
	if len(errs) != 1 || errs[0].Field != "spec.serviceRef" || errs[0].Type != field.ErrorTypeRequired {
		t.Errorf("want serviceRef required for a Usage pricing, got %v", errs)
	}
}

func TestValidateServicePricingCreate_BasedOn(t *testing.T) {
	const compute = "compute.datumapis.com"
	tier := func(value, rate string) billingv1alpha1.PricingRate {
		return billingv1alpha1.PricingRate{
			Match: &billingv1alpha1.DimensionMatch{Dimension: "tier", Value: value},
			Flat:  rate,
		}
	}
	tiered := func(upTo, low, high string) billingv1alpha1.PricingRate {
		return billingv1alpha1.PricingRate{Tiered: []billingv1alpha1.PricingTierBand{
			{UpTo: upTo, Rate: low}, {Rate: high},
		}}
	}
	list := usagePricing(compute, tier("standard", "0.025"), tiered("100", "0.03", "0.02"))
	customer := func(mutate func(*billingv1alpha1.ServicePricingSpec)) *billingv1alpha1.ServicePricing {
		sp := usagePricing(compute, tier("standard", "0.020"), tiered("100", "0.025", "0.015"))
		sp.Name = "contract-acme-compute-cpu"
		sp.Spec.BasedOn = list.Name
		if mutate != nil {
			mutate(&sp.Spec)
		}
		return sp
	}
	fee := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "setup-fee", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType: billingv1alpha1.ChargeTypeOneTime,
			Currency:   "USD",
			Amount:     "1500.00",
			Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
		},
	}
	basedOnCustomer := customer(nil)

	tests := []struct {
		name      string
		pricing   *billingv1alpha1.ServicePricing
		wantField string
	}{
		{name: "only rate amounts differ", pricing: customer(nil)},
		{
			name:      "list pricing missing",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.BasedOn = "nope" }),
			wantField: "spec.basedOn",
		},
		{
			name:      "based on a fee",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.BasedOn = fee.Name }),
			wantField: "spec.basedOn",
		},
		{
			name:      "based on a customer pricing",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.BasedOn = basedOnCustomer.Name }),
			wantField: "spec.basedOn",
		},
		{
			name:      "different pricing unit",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.PricingUnit = "vcpu-hour" }),
			wantField: "spec.pricingUnit",
		},
		{
			name: "different unit conversion",
			pricing: customer(func(s *billingv1alpha1.ServicePricingSpec) {
				s.UnitConversion = &billingv1alpha1.UnitConversion{MeterUnitsPerPricingUnit: "1000", Description: "x"}
			}),
			wantField: "spec.unitConversion",
		},
		{
			name: "rates in a different order",
			pricing: customer(func(s *billingv1alpha1.ServicePricingSpec) {
				s.Rates[0].Match.Value = "premium"
			}),
			wantField: "spec.rates[0].match",
		},
		{
			name: "fewer rates",
			pricing: customer(func(s *billingv1alpha1.ServicePricingSpec) {
				s.Rates = s.Rates[:1]
			}),
			wantField: "spec.rates",
		},
		{
			name: "flat instead of tiered",
			pricing: customer(func(s *billingv1alpha1.ServicePricingSpec) {
				s.Rates[1] = billingv1alpha1.PricingRate{Flat: "0.01"}
			}),
			wantField: "spec.rates[1]",
		},
		{
			name: "different tier bound",
			pricing: customer(func(s *billingv1alpha1.ServicePricingSpec) {
				s.Rates[1].Tiered[0].UpTo = "200"
			}),
			wantField: "spec.rates[1].tiered[0].upTo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// basedOnCustomer is stored as if created earlier, so a
			// pricing can name it.
			stored := basedOnCustomer.DeepCopy()
			stored.Name = "customer-earlier"
			if tc.pricing.Spec.BasedOn == basedOnCustomer.Name {
				tc.pricing.Spec.BasedOn = stored.Name
			}
			cl := newPricingClient(testMeter(billingv1alpha1.PhasePublished, compute), list, fee, stored)
			errs := ValidateServicePricingCreate(context.Background(), cl, tc.pricing)
			if tc.wantField == "" {
				if len(errs) != 0 {
					t.Errorf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != tc.wantField {
				t.Errorf("want one error on %s, got %v", tc.wantField, errs)
			}
		})
	}

	t.Run("fee based on a Usage pricing", func(t *testing.T) {
		withBasedOn := fee.DeepCopy()
		withBasedOn.Spec.BasedOn = list.Name
		errs := ValidateServicePricingCreate(context.Background(), newPricingClient(list), withBasedOn)
		if len(errs) != 1 || errs[0].Field != "spec.basedOn" {
			t.Errorf("want basedOn refused across charge types, got %v", errs)
		}
	})
}

func TestValidateServicePricingCreate_FeeBasedOn(t *testing.T) {
	const bareMetal = "bare-metal.datumapis.com"
	listFee := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-connect-v1", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeRecurring,
			ServiceRef:  bareMetal,
			Currency:    "USD",
			DisplayName: "Cross connect",
			Amount:      "300.00",
			Interval:    billingv1alpha1.ChargeIntervalMonthly,
			PricingUnit: "port",
		},
	}
	setupFee := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "setup-v1", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType: billingv1alpha1.ChargeTypeOneTime,
			Currency:   "USD",
			Amount:     "1500.00",
			Trigger:    billingv1alpha1.ChargeTriggerBillingAccountActivation,
		},
	}
	customerFee := listFee.DeepCopy()
	customerFee.Name = "contract-acme-fee-1"
	customerFee.Spec.BasedOn = listFee.Name
	customerFee.Spec.Amount = "250.00"
	customerFee.Spec.DisplayName = "Cross connect (Acme)"
	storedCustomer := customerFee.DeepCopy()
	storedCustomer.Name = "contract-acme-earlier-fee-1"

	customer := func(mutate func(*billingv1alpha1.ServicePricingSpec)) *billingv1alpha1.ServicePricing {
		sp := customerFee.DeepCopy()
		if mutate != nil {
			mutate(&sp.Spec)
		}
		return sp
	}

	tests := []struct {
		name      string
		pricing   *billingv1alpha1.ServicePricing
		wantField string
	}{
		{name: "only the amount and name differ", pricing: customer(nil)},
		{
			name:      "based on a one-time fee",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.BasedOn = setupFee.Name }),
			wantField: "spec.basedOn",
		},
		{
			name:      "based on a customer pricing",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.BasedOn = storedCustomer.Name }),
			wantField: "spec.basedOn",
		},
		{
			name:      "different service",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.ServiceRef = "" }),
			wantField: "spec.serviceRef",
		},
		{
			name:      "different unit",
			pricing:   customer(func(s *billingv1alpha1.ServicePricingSpec) { s.PricingUnit = "cable" }),
			wantField: "spec.pricingUnit",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := newPricingClient(testService(bareMetal), listFee, setupFee, storedCustomer)
			errs := ValidateServicePricingCreate(context.Background(), cl, tc.pricing)
			if tc.wantField == "" {
				if len(errs) != 0 {
					t.Errorf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != tc.wantField {
				t.Errorf("want one error on %s, got %v", tc.wantField, errs)
			}
		})
	}

	t.Run("different trigger", func(t *testing.T) {
		// Only one trigger exists today, so compare against a list fee with none.
		errs := validateBasedOnSpec(
			&billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeOneTime, Currency: "USD", Amount: "1000.00",
				Trigger: billingv1alpha1.ChargeTriggerBillingAccountActivation, BasedOn: setupFee.Name,
			},
			&billingv1alpha1.ServicePricingSpec{
				ChargeType: billingv1alpha1.ChargeTypeOneTime, Currency: "USD", Amount: "1500.00",
			},
			field.NewPath("spec"),
		)
		if len(errs) != 1 || errs[0].Field != "spec.trigger" {
			t.Errorf("want one error on spec.trigger, got %v", errs)
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
