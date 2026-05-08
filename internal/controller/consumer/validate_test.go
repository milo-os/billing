// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/event"
)

func newTestMeterCache(mds ...billingv1alpha1.MeterDefinition) *MeterDefinitionCache {
	mc := &MeterDefinitionCache{byMeterName: make(map[string]*billingv1alpha1.MeterDefinition)}
	for i := range mds {
		mc.upsert(&mds[i])
	}
	return mc
}

func publishedMD(name, meterName string, dims []string) billingv1alpha1.MeterDefinition {
	return billingv1alpha1.MeterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: billingv1alpha1.MeterDefinitionSpec{
			MeterName:   meterName,
			Phase:       billingv1alpha1.PhasePublished,
			DisplayName: name,
			Measurement: billingv1alpha1.MeterMeasurement{
				Aggregation: billingv1alpha1.MeterAggregationSum,
				Unit:        "s",
				Dimensions:  dims,
			},
			Billing: billingv1alpha1.MeterBilling{
				ConsumedUnit: "s",
				PricingUnit:  "h",
			},
			MonitoredResourceTypes: []string{"instance"},
		},
	}
}

func phasedMD(name, meterName string, phase billingv1alpha1.Phase) billingv1alpha1.MeterDefinition {
	md := publishedMD(name, meterName, nil)
	md.Spec.Phase = phase
	return md
}

func newTestCE(eventType string) *cloudevents.Event {
	ce := cloudevents.NewEvent()
	ce.SetID("test-id")
	ce.SetType(eventType)
	ce.SetSource("/test")
	return &ce
}

func newTestCEWithDimensions(eventType string, dims map[string]string) *cloudevents.Event {
	ce := newTestCE(eventType)
	_ = ce.SetData("application/json", event.EventData{Dimensions: dims})
	return ce
}

func TestValidate_Published_NoExtraDimensions(t *testing.T) {
	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region", "tier"})
	mc := newTestMeterCache(md)

	ce := newTestCEWithDimensions("compute.miloapis.com/instance/cpu-seconds", map[string]string{"region": "us-east-1"})

	result := validate(ce, mc)
	if !result.OK {
		t.Errorf("expected OK=true, got reason=%s", result.Reason)
	}
	if result.MeterDefinition == nil || result.MeterDefinition.Spec.MeterName != md.Spec.MeterName {
		t.Errorf("unexpected MeterDefinition: %v", result.MeterDefinition)
	}
}

func TestValidate_Deprecated_StillPassesValidation(t *testing.T) {
	md := phasedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", billingv1alpha1.PhaseDeprecated)
	md.Spec.Measurement = billingv1alpha1.MeterMeasurement{
		Aggregation: billingv1alpha1.MeterAggregationSum,
		Unit:        "s",
		Dimensions:  []string{"region"},
	}
	md.Spec.Billing = billingv1alpha1.MeterBilling{ConsumedUnit: "s", PricingUnit: "h"}
	md.Spec.MonitoredResourceTypes = []string{"instance"}
	mc := newTestMeterCache(md)

	ce := newTestCEWithDimensions("compute.miloapis.com/instance/cpu-seconds", map[string]string{"region": "us-east-1"})

	result := validate(ce, mc)
	if !result.OK {
		t.Errorf("expected Deprecated meter to pass validation, got reason=%s", result.Reason)
	}
}

func TestValidate_DraftMeter_QuarantinesUnknownMeter(t *testing.T) {
	md := phasedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", billingv1alpha1.PhaseDraft)
	mc := newTestMeterCache(md)

	result := validate(newTestCE("compute.miloapis.com/instance/cpu-seconds"), mc)
	if result.OK {
		t.Error("expected validation to fail for Draft meter")
	}
	if result.Reason != ReasonUnknownMeter {
		t.Errorf("expected ReasonUnknownMeter, got %s", result.Reason)
	}
}

func TestValidate_RetiredMeter_QuarantinesUnknownMeter(t *testing.T) {
	md := phasedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", billingv1alpha1.PhaseRetired)
	mc := newTestMeterCache(md)

	result := validate(newTestCE("compute.miloapis.com/instance/cpu-seconds"), mc)
	if result.OK {
		t.Error("expected validation to fail for Retired meter")
	}
	if result.Reason != ReasonUnknownMeter {
		t.Errorf("expected ReasonUnknownMeter, got %s", result.Reason)
	}
}

func TestValidate_MissingMeter_QuarantinesUnknownMeter(t *testing.T) {
	mc := newTestMeterCache() // empty

	result := validate(newTestCE("compute.miloapis.com/instance/cpu-seconds"), mc)
	if result.OK {
		t.Error("expected validation to fail when no MeterDefinition exists")
	}
	if result.Reason != ReasonUnknownMeter {
		t.Errorf("expected ReasonUnknownMeter, got %s", result.Reason)
	}
}

func TestValidate_ExtraDimensionKey_QuarantinesInvalidDimensions(t *testing.T) {
	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region"})
	mc := newTestMeterCache(md)

	ce := newTestCEWithDimensions("compute.miloapis.com/instance/cpu-seconds", map[string]string{
		"region":   "us-east-1",
		"unknownX": "value",
	})

	result := validate(ce, mc)
	if result.OK {
		t.Error("expected validation to fail for unknown dimension key")
	}
	if result.Reason != ReasonInvalidDimensions {
		t.Errorf("expected ReasonInvalidDimensions, got %s", result.Reason)
	}
}

func TestValidate_NoDimensions_WithDeclaredDims_Passes(t *testing.T) {
	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region", "tier"})
	mc := newTestMeterCache(md)

	result := validate(newTestCE("compute.miloapis.com/instance/cpu-seconds"), mc)
	if !result.OK {
		t.Errorf("expected OK=true for event with no dimensions, got reason=%s", result.Reason)
	}
}
