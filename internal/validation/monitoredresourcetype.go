// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateMonitoredResourceTypeCreate validates a MonitoredResourceType on creation.
func ValidateMonitoredResourceTypeCreate(mrt *billingv1alpha1.MonitoredResourceType) field.ErrorList {
	// No create-specific validation beyond what CRD markers enforce.
	return nil
}

// ValidateMonitoredResourceTypeUpdate validates a MonitoredResourceType on update.
func ValidateMonitoredResourceTypeUpdate(oldMRT, newMRT *billingv1alpha1.MonitoredResourceType) field.ErrorList {
	var allErrs field.ErrorList

	specPath := field.NewPath("spec")

	// resourceTypeName is immutable (also enforced by CEL)
	if oldMRT.Spec.ResourceTypeName != newMRT.Spec.ResourceTypeName {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("resourceTypeName"),
			"resourceTypeName is immutable",
		))
	}

	// gvk.group is immutable
	if oldMRT.Spec.GVK.Group != newMRT.Spec.GVK.Group {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("gvk", "group"),
			"gvk.group is immutable",
		))
	}

	// gvk.kind is immutable
	if oldMRT.Spec.GVK.Kind != newMRT.Spec.GVK.Kind {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("gvk", "kind"),
			"gvk.kind is immutable",
		))
	}

	// labels is additive only (cannot remove entries by name)
	allErrs = append(allErrs, validateLabelsAdditive(
		oldMRT.Spec.Labels,
		newMRT.Spec.Labels,
		specPath.Child("labels"),
	)...)

	// phase transitions are forward-only
	allErrs = append(allErrs, validatePhaseTransition(
		oldMRT.Spec.Phase,
		newMRT.Spec.Phase,
		specPath.Child("phase"),
	)...)

	return allErrs
}

// validateLabelsAdditive checks that no existing labels were removed by name.
func validateLabelsAdditive(oldLabels, newLabels []billingv1alpha1.MonitoredResourceLabel, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	newSet := make(map[string]bool, len(newLabels))
	for _, l := range newLabels {
		newSet[l.Name] = true
	}

	for _, l := range oldLabels {
		if !newSet[l.Name] {
			allErrs = append(allErrs, field.Forbidden(
				fldPath,
				"labels is additive only; removing label \""+l.Name+"\" is not allowed",
			))
		}
	}

	return allErrs
}
