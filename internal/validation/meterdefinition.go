// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateMeterDefinitionCreate validates a MeterDefinition on creation.
func ValidateMeterDefinitionCreate(md *billingv1alpha1.MeterDefinition) field.ErrorList {
	// No create-specific validation beyond what CRD markers enforce.
	return nil
}

// ValidateMeterDefinitionUpdate validates a MeterDefinition on update.
func ValidateMeterDefinitionUpdate(oldMD, newMD *billingv1alpha1.MeterDefinition) field.ErrorList {
	var allErrs field.ErrorList

	specPath := field.NewPath("spec")

	// meterName is immutable (also enforced by CEL, belt-and-suspenders here)
	if oldMD.Spec.MeterName != newMD.Spec.MeterName {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("meterName"),
			"meterName is immutable",
		))
	}

	// measurement.aggregation is immutable
	if oldMD.Spec.Measurement.Aggregation != newMD.Spec.Measurement.Aggregation {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("measurement", "aggregation"),
			"measurement.aggregation is immutable",
		))
	}

	// measurement.unit is immutable
	if oldMD.Spec.Measurement.Unit != newMD.Spec.Measurement.Unit {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("measurement", "unit"),
			"measurement.unit is immutable",
		))
	}

	// measurement.dimensions is additive only
	allErrs = append(allErrs, validateDimensionsAdditive(
		oldMD.Spec.Measurement.Dimensions,
		newMD.Spec.Measurement.Dimensions,
		specPath.Child("measurement", "dimensions"),
	)...)

	// phase transitions are forward-only
	allErrs = append(allErrs, validatePhaseTransition(
		oldMD.Spec.Phase,
		newMD.Spec.Phase,
		specPath.Child("phase"),
	)...)

	return allErrs
}

// validateDimensionsAdditive checks that no existing dimensions were removed.
func validateDimensionsAdditive(oldDims, newDims []string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	newSet := make(map[string]bool, len(newDims))
	for _, d := range newDims {
		newSet[d] = true
	}

	for _, d := range oldDims {
		if !newSet[d] {
			allErrs = append(allErrs, field.Forbidden(
				fldPath,
				"measurement.dimensions is additive only; removing dimension \""+d+"\" is not allowed",
			))
		}
	}

	return allErrs
}

// validatePhaseTransition enforces forward-only phase transitions:
// Draft -> Published -> Deprecated -> Retired.
func validatePhaseTransition(oldPhase, newPhase billingv1alpha1.Phase, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if oldPhase == newPhase {
		return allErrs
	}

	validTransitions := map[billingv1alpha1.Phase]billingv1alpha1.Phase{
		billingv1alpha1.PhaseDraft:      billingv1alpha1.PhasePublished,
		billingv1alpha1.PhasePublished:  billingv1alpha1.PhaseDeprecated,
		billingv1alpha1.PhaseDeprecated: billingv1alpha1.PhaseRetired,
	}

	expected, ok := validTransitions[oldPhase]
	if !ok || expected != newPhase {
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			"invalid phase transition from \""+string(oldPhase)+"\" to \""+string(newPhase)+"\"; allowed forward transitions are Draft→Published, Published→Deprecated, Deprecated→Retired",
		))
	}

	return allErrs
}
