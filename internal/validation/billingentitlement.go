// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateBillingEntitlementCreate validates a BillingEntitlement on creation.
func ValidateBillingEntitlementCreate(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, be)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, be)...)

	return allErrs
}

// ValidateBillingEntitlementUpdate validates a BillingEntitlement on update.
// Changing offerRef is allowed (staff switch); uniqueness and GA checks still apply.
func ValidateBillingEntitlementUpdate(
	ctx context.Context,
	c client.Client,
	_, newBE *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, newBE)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, newBE)...)

	return allErrs
}

func validateBillingEntitlementOfferRef(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "offerRef", "name")

	if be.Spec.OfferRef.Name == "" {
		allErrs = append(allErrs, field.Required(fldPath, "offerRef.name is required"))
		return allErrs
	}

	var offer billingv1alpha1.Offer
	if err := c.Get(ctx, types.NamespacedName{Name: be.Spec.OfferRef.Name}, &offer); err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(fldPath, be.Spec.OfferRef.Name))
		} else {
			allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("reading offer: %w", err)))
		}
		return allErrs
	}

	if offer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			be.Spec.OfferRef.Name,
			fmt.Sprintf("offer %q has launchStage %q; only GA Offers may be assigned", offer.Name, offer.Spec.LaunchStage),
		))
	}

	return allErrs
}

// validateSingleBillingEntitlementPerAccount enforces at most one non-deleting
// BillingEntitlement per billingAccountRef in the same namespace.
func validateSingleBillingEntitlementPerAccount(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "billingAccountRef", "name")

	if be.Spec.BillingAccountRef.Name == "" {
		return allErrs
	}

	var list billingv1alpha1.BillingEntitlementList
	if err := c.List(ctx, &list, client.InNamespace(be.Namespace)); err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath,
			fmt.Errorf("listing billing entitlements: %w", err)))
		return allErrs
	}

	for i := range list.Items {
		existing := &list.Items[i]
		if existing.UID == be.UID {
			continue
		}
		if !existing.DeletionTimestamp.IsZero() {
			continue
		}
		if existing.Spec.BillingAccountRef.Name != be.Spec.BillingAccountRef.Name {
			continue
		}
		allErrs = append(allErrs, field.Forbidden(
			fldPath,
			fmt.Sprintf("billing account %q already has billing entitlement %q",
				be.Spec.BillingAccountRef.Name, existing.Name),
		))
		break
	}

	return allErrs
}
