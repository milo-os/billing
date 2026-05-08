// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	cloudevents "github.com/cloudevents/sdk-go/v2"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/event"
)

// QuarantineReason is the rejection reason used for metrics and subject routing.
type QuarantineReason string

const (
	// ReasonUnknownMeter indicates no Published MeterDefinition matched the event type.
	ReasonUnknownMeter QuarantineReason = "unknown_meter"

	// ReasonInvalidDimensions indicates the event contains dimension keys not
	// declared on the MeterDefinition.
	ReasonInvalidDimensions QuarantineReason = "invalid_dimensions"

	// ReasonAttributionFailure indicates no Active BillingAccountBinding exists
	// for the project, or the matched account is not Ready.
	ReasonAttributionFailure QuarantineReason = "attribution_failure"
)

// ValidationResult carries the outcome of the validate step.
type ValidationResult struct {
	// OK is true when the event passed validation.
	OK bool
	// Reason is set when OK is false.
	Reason QuarantineReason
	// MeterDefinition is the matched MeterDefinition when OK is true.
	MeterDefinition *billingv1alpha1.MeterDefinition
}

// validate checks that ce.Type() matches a Published or Deprecated
// MeterDefinition and that data.dimensions keys are a declared subset.
// Lookup is a pure in-memory map read — no API server calls.
func validate(ce *cloudevents.Event, mc *MeterDefinitionCache) ValidationResult {
	md := mc.Get(ce.Type())
	if md == nil {
		return ValidationResult{OK: false, Reason: ReasonUnknownMeter}
	}

	var data event.EventData
	// DataAs returns nil when the event carries no data — treat as empty dimensions.
	_ = ce.DataAs(&data)

	declaredDimensions := make(map[string]struct{}, len(md.Spec.Measurement.Dimensions))
	for _, dim := range md.Spec.Measurement.Dimensions {
		declaredDimensions[dim] = struct{}{}
	}

	for key := range data.Dimensions {
		if _, ok := declaredDimensions[key]; !ok {
			return ValidationResult{OK: false, Reason: ReasonInvalidDimensions}
		}
	}

	return ValidationResult{OK: true, MeterDefinition: md}
}
