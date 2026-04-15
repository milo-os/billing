// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func newMeterDefinition(name, meterName, owner string, dims ...string) *billingv1alpha1.MeterDefinition {
	return &billingv1alpha1.MeterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: billingv1alpha1.MeterDefinitionSpec{
			MeterName:   meterName,
			DisplayName: name,
			Owner:       billingv1alpha1.MeterOwner{Service: owner},
			Measurement: billingv1alpha1.MeterMeasurement{
				Aggregation: billingv1alpha1.MeterAggregationSum,
				Unit:        "s",
				Dimensions:  dims,
			},
			Billing: billingv1alpha1.MeterBilling{
				ConsumedUnit:   "s",
				PricingUnit:    "h",
				ChargeCategory: billingv1alpha1.MeterChargeCategoryUsage,
			},
		},
	}
}

func TestValidateMeterNameFormat(t *testing.T) {
	tests := []struct {
		name      string
		meterName string
		owner     string
		wantErr   bool
	}{
		{
			name:      "valid prefix",
			meterName: "compute.miloapis.com/instance/cpu-seconds",
			owner:     "compute.miloapis.com",
			wantErr:   false,
		},
		{
			name:      "missing owner prefix",
			meterName: "something/else",
			owner:     "compute.miloapis.com",
			wantErr:   true,
		},
		{
			name:      "exact owner without path segment",
			meterName: "compute.miloapis.com/",
			owner:     "compute.miloapis.com",
			wantErr:   true,
		},
		{
			name:      "wrong owner prefix",
			meterName: "storage.miloapis.com/bucket/ops",
			owner:     "compute.miloapis.com",
			wantErr:   true,
		},
		{
			name:      "owner missing skips prefix check",
			meterName: "whatever/here",
			owner:     "",
			wantErr:   false,
		},
		{
			name:      "owner equal to meterName without slash",
			meterName: "compute.miloapis.com",
			owner:     "compute.miloapis.com",
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := newMeterDefinition("m", tt.meterName, tt.owner)
			errs := validateMeterNameFormat(md)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateMeterNameFormat() errs = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateDimensionsAdditive(t *testing.T) {
	tests := []struct {
		name    string
		oldDims []string
		newDims []string
		wantErr bool
	}{
		{
			name:    "no dimensions",
			oldDims: nil,
			newDims: nil,
			wantErr: false,
		},
		{
			name:    "add a dimension",
			oldDims: []string{"region"},
			newDims: []string{"region", "tier"},
			wantErr: false,
		},
		{
			name:    "unchanged",
			oldDims: []string{"region", "tier"},
			newDims: []string{"region", "tier"},
			wantErr: false,
		},
		{
			name:    "remove a dimension",
			oldDims: []string{"region", "tier"},
			newDims: []string{"region"},
			wantErr: true,
		},
		{
			name:    "reorder",
			oldDims: []string{"region", "tier"},
			newDims: []string{"tier", "region"},
			wantErr: true,
		},
		{
			name:    "rename",
			oldDims: []string{"region"},
			newDims: []string{"zone"},
			wantErr: true,
		},
		{
			name:    "add to empty",
			oldDims: nil,
			newDims: []string{"region"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldMD := newMeterDefinition("m", "svc/m", "svc", tt.oldDims...)
			newMD := newMeterDefinition("m", "svc/m", "svc", tt.newDims...)
			errs := validateDimensionsAdditive(oldMD, newMD)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateDimensionsAdditive() errs = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateMeterDefinitionUpdate_Immutability(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(md *billingv1alpha1.MeterDefinition)
		wantErr bool
	}{
		{
			name:    "no changes",
			mutate:  func(md *billingv1alpha1.MeterDefinition) {},
			wantErr: false,
		},
		{
			name:    "change meterName",
			mutate:  func(md *billingv1alpha1.MeterDefinition) { md.Spec.MeterName = "svc/other" },
			wantErr: true,
		},
		{
			name:    "change owner.service",
			mutate:  func(md *billingv1alpha1.MeterDefinition) { md.Spec.Owner.Service = "other" },
			wantErr: true,
		},
		{
			name: "change aggregation",
			mutate: func(md *billingv1alpha1.MeterDefinition) {
				md.Spec.Measurement.Aggregation = billingv1alpha1.MeterAggregationMax
			},
			wantErr: true,
		},
		{
			name:    "change unit",
			mutate:  func(md *billingv1alpha1.MeterDefinition) { md.Spec.Measurement.Unit = "By" },
			wantErr: true,
		},
		{
			name:    "change displayName (editable)",
			mutate:  func(md *billingv1alpha1.MeterDefinition) { md.Spec.DisplayName = "new name" },
			wantErr: false,
		},
		{
			name:    "change description (editable)",
			mutate:  func(md *billingv1alpha1.MeterDefinition) { md.Spec.Description = "updated" },
			wantErr: false,
		},
		{
			name: "change pricingUnit (editable)",
			mutate: func(md *billingv1alpha1.MeterDefinition) {
				md.Spec.Billing.PricingUnit = "min"
			},
			wantErr: false,
		},
	}

	// Client is unused for these fields but required by the options struct.
	cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	opts := MeterDefinitionValidationOptions{Context: context.Background(), Client: cl}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldMD := newMeterDefinition("m", "svc/m", "svc", "region")
			newMD := oldMD.DeepCopy()
			tt.mutate(newMD)
			errs := ValidateMeterDefinitionUpdate(oldMD, newMD, opts)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateMeterDefinitionUpdate() errs = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

