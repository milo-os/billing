// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// meterDefinitionMeterNameField is the field index key used to look up
// MeterDefinitions by their canonical meter name. Kept here as a string
// literal so the validation package doesn't depend on the controller
// package.
const meterDefinitionMeterNameField = ".spec.meterName"

// MeterDefinitionValidationOptions contains the context needed for
// cross-resource validation of a MeterDefinition.
type MeterDefinitionValidationOptions struct {
	Context context.Context
	Client  client.Client
}

// ValidateMeterDefinitionCreate validates a MeterDefinition on creation.
func ValidateMeterDefinitionCreate(
	md *billingv1alpha1.MeterDefinition,
	opts MeterDefinitionValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateMeterNameFormat(md)...)
	allErrs = append(allErrs, validateMeterNameUnique(md, opts)...)

	return allErrs
}

// ValidateMeterDefinitionUpdate validates a MeterDefinition on update.
// Core fields are enforced as immutable via CEL XValidation rules on the
// CRD; the webhook provides belt-and-suspenders checks and enforces the
// additive-only dimensions rule that CEL can't express cleanly.
func ValidateMeterDefinitionUpdate(
	oldMD, newMD *billingv1alpha1.MeterDefinition,
	opts MeterDefinitionValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList

	if oldMD.Spec.MeterName != newMD.Spec.MeterName {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "meterName"),
			"meterName is immutable",
		))
	}
	if oldMD.Spec.Owner.Service != newMD.Spec.Owner.Service {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "owner", "service"),
			"owner.service is immutable",
		))
	}
	if oldMD.Spec.Measurement.Aggregation != newMD.Spec.Measurement.Aggregation {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "measurement", "aggregation"),
			"measurement.aggregation is immutable",
		))
	}
	if oldMD.Spec.Measurement.Unit != newMD.Spec.Measurement.Unit {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "measurement", "unit"),
			"measurement.unit is immutable",
		))
	}

	allErrs = append(allErrs, validateDimensionsAdditive(oldMD, newMD)...)

	return allErrs
}

// validateMeterNameFormat enforces that spec.meterName is prefixed by the
// owning service and follows a "<service>/<path>" shape.
func validateMeterNameFormat(md *billingv1alpha1.MeterDefinition) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "meterName")

	name := md.Spec.MeterName
	owner := md.Spec.Owner.Service

	if owner == "" {
		// owner.service is required by the CRD schema; if missing, skip
		// prefix validation to avoid a confusing double error.
		return allErrs
	}

	prefix := owner + "/"
	if !strings.HasPrefix(name, prefix) {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			name,
			fmt.Sprintf("must be prefixed with the owning service %q (e.g. %q)",
				prefix, prefix+"example-meter"),
		))
		return allErrs
	}

	suffix := strings.TrimPrefix(name, prefix)
	if suffix == "" {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			name,
			"must include a path segment after the owning service prefix",
		))
	}

	return allErrs
}

// validateMeterNameUnique checks that no other MeterDefinition has the
// same spec.meterName. Uses the field index when available; falls back to
// a full list for environments where the index isn't registered (tests).
func validateMeterNameUnique(
	md *billingv1alpha1.MeterDefinition,
	opts MeterDefinitionValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "meterName")

	var list billingv1alpha1.MeterDefinitionList
	if err := opts.Client.List(opts.Context, &list,
		client.MatchingFields{meterDefinitionMeterNameField: md.Spec.MeterName},
	); err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath,
			fmt.Errorf("failed to list existing meter definitions: %w", err)))
		return allErrs
	}

	for i := range list.Items {
		existing := &list.Items[i]
		if existing.UID == md.UID {
			continue
		}
		allErrs = append(allErrs, field.Duplicate(fldPath, md.Spec.MeterName))
		break
	}

	return allErrs
}

// validateDimensionsAdditive enforces that dimensions can be added but
// not removed or reordered. Downstream aggregations depend on the
// declared dimension list; removals are breaking changes that must ship
// as a new meter.
func validateDimensionsAdditive(oldMD, newMD *billingv1alpha1.MeterDefinition) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "measurement", "dimensions")

	oldDims := oldMD.Spec.Measurement.Dimensions
	newDims := newMD.Spec.Measurement.Dimensions

	if len(newDims) < len(oldDims) {
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			"dimensions may only be added; removal is a breaking change and requires a new meter",
		))
		return allErrs
	}

	for i, old := range oldDims {
		if newDims[i] != old {
			allErrs = append(allErrs, field.Forbidden(
				fldPath.Index(i),
				fmt.Sprintf("existing dimension %q cannot be changed or reordered", old),
			))
		}
	}

	return allErrs
}
