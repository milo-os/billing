// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// newMeterDefinition is a test helper that builds a minimal valid MeterDefinition.
func newMeterDefinition(name, meterName string, dims ...string) *billingv1alpha1.MeterDefinition {
	return &billingv1alpha1.MeterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: billingv1alpha1.MeterDefinitionSpec{
			MeterName:   meterName,
			DisplayName: name,
			Phase:       billingv1alpha1.PhaseDraft,
			Measurement: billingv1alpha1.MeterMeasurement{
				Aggregation: billingv1alpha1.MeterAggregationSum,
				Unit:        "s",
				Dimensions:  dims,
			},
			Billing: billingv1alpha1.MeterBilling{
				ConsumedUnit: "s",
				PricingUnit:  "h",
			},
		},
	}
}

func TestValidateMeterDefinitionCreate(t *testing.T) {
	md := newMeterDefinition("test-meter", "compute.miloapis.com/instance/cpu-seconds", "region")
	errs := ValidateMeterDefinitionCreate(md)
	if len(errs) != 0 {
		t.Errorf("expected no errors for valid MeterDefinition, got: %v", errs)
	}
}

func TestValidateDimensionsAdditive(t *testing.T) {
	tests := []struct {
		name     string
		oldDims  []string
		newDims  []string
		wantErrs int
	}{
		{
			name:     "nil to nil ok",
			oldDims:  nil,
			newDims:  nil,
			wantErrs: 0,
		},
		{
			name:     "add dimension ok",
			oldDims:  []string{"region"},
			newDims:  []string{"region", "instance.type"},
			wantErrs: 0,
		},
		{
			name:     "unchanged ok",
			oldDims:  []string{"region", "instance.type"},
			newDims:  []string{"region", "instance.type"},
			wantErrs: 0,
		},
		{
			name:     "remove dimension errors",
			oldDims:  []string{"region", "instance.type"},
			newDims:  []string{"region"},
			wantErrs: 1,
		},
		{
			name:     "reorder ok (all old dims present)",
			oldDims:  []string{"region", "instance.type"},
			newDims:  []string{"instance.type", "region"},
			wantErrs: 0,
		},
		{
			name:     "rename dimension errors",
			oldDims:  []string{"region"},
			newDims:  []string{"zone"},
			wantErrs: 1,
		},
		{
			name:     "add to empty ok",
			oldDims:  nil,
			newDims:  []string{"region"},
			wantErrs: 0,
		},
	}

	fldPath := field.NewPath("spec", "measurement", "dimensions")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateDimensionsAdditive(tc.oldDims, tc.newDims, fldPath)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}

func TestValidateMeterDefinitionUpdate_Immutability(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition
		wantErrs int
	}{
		{
			name: "no changes ok",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				return md.DeepCopy()
			},
			wantErrs: 0,
		},
		{
			name: "changing meterName errors",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.MeterName = "compute.miloapis.com/instance/cpu-seconds/v2"
				return c
			},
			wantErrs: 1,
		},
		{
			name: "changing aggregation errors",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Measurement.Aggregation = billingv1alpha1.MeterAggregationMax
				return c
			},
			wantErrs: 1,
		},
		{
			name: "changing unit errors",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Measurement.Unit = "h"
				return c
			},
			wantErrs: 1,
		},
		{
			name: "changing displayName ok",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.DisplayName = "Updated Display Name"
				return c
			},
			wantErrs: 0,
		},
		{
			name: "changing description ok",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Description = "Updated description."
				return c
			},
			wantErrs: 0,
		},
		{
			name: "changing pricingUnit ok",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Billing.PricingUnit = "d"
				return c
			},
			wantErrs: 0,
		},
		{
			name: "valid phase transition ok",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Phase = billingv1alpha1.PhasePublished
				return c
			},
			wantErrs: 0,
		},
		{
			name: "invalid phase transition errors",
			mutate: func(md *billingv1alpha1.MeterDefinition) *billingv1alpha1.MeterDefinition {
				c := md.DeepCopy()
				c.Spec.Phase = billingv1alpha1.PhaseRetired // Draft -> Retired is invalid
				return c
			},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldMD := newMeterDefinition("test-meter", "compute.miloapis.com/instance/cpu-seconds", "region")
			newMD := tc.mutate(oldMD)
			errs := ValidateMeterDefinitionUpdate(oldMD, newMD)
			if len(errs) != tc.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tc.wantErrs, errs)
			}
		})
	}
}
