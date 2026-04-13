// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// BillingAccountBindingValidationOptions contains the context needed for
// cross-resource validation of a BillingAccountBinding.
type BillingAccountBindingValidationOptions struct {
	Context context.Context
	Client  client.Client
}

// ValidateBillingAccountBindingCreate validates a BillingAccountBinding on creation.
func ValidateBillingAccountBindingCreate(
	binding *billingv1alpha1.BillingAccountBinding,
	opts BillingAccountBindingValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList

	// 1. Validate referenced billing account exists and is Ready
	allErrs = append(allErrs, validateBillingAccountReady(binding, opts)...)

	// 2. Validate single active binding per project
	allErrs = append(allErrs, validateSingleActiveBinding(binding, opts)...)

	return allErrs
}

// validateBillingAccountReady checks that the referenced BillingAccount exists
// and is in Ready phase.
func validateBillingAccountReady(
	binding *billingv1alpha1.BillingAccountBinding,
	opts BillingAccountBindingValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "billingAccountRef", "name")

	var account billingv1alpha1.BillingAccount
	accountKey := client.ObjectKey{
		Namespace: binding.Namespace,
		Name:      binding.Spec.BillingAccountRef.Name,
	}

	if err := opts.Client.Get(opts.Context, accountKey, &account); err != nil {
		allErrs = append(allErrs, field.NotFound(fldPath, binding.Spec.BillingAccountRef.Name))
		return allErrs
	}

	if !account.DeletionTimestamp.IsZero() {
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			fmt.Sprintf("billing account %q is being deleted", account.Name),
		))
		return allErrs
	}

	if account.Status.Phase != billingv1alpha1.BillingAccountPhaseReady {
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			fmt.Sprintf("billing account %q is not ready (current phase: %s)", account.Name, account.Status.Phase),
		))
	}

	return allErrs
}

// validateSingleActiveBinding checks that no other active binding exists for
// the same project in the same namespace.
func validateSingleActiveBinding(
	binding *billingv1alpha1.BillingAccountBinding,
	opts BillingAccountBindingValidationOptions,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "projectRef", "name")

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := opts.Client.List(opts.Context, &bindingList,
		client.InNamespace(binding.Namespace),
		client.MatchingFields{".spec.projectRef.name": binding.Spec.ProjectRef.Name},
	); err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath,
			fmt.Errorf("failed to list existing bindings: %w", err)))
		return allErrs
	}

	for i := range bindingList.Items {
		existing := &bindingList.Items[i]
		if existing.UID == binding.UID {
			continue
		}
		if existing.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			allErrs = append(allErrs, field.Forbidden(
				fldPath,
				fmt.Sprintf("project %q already has an active billing account binding %q",
					binding.Spec.ProjectRef.Name, existing.Name),
			))
			break
		}
	}

	return allErrs
}
