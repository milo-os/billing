// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// Bounds on a contract's start, relative to when its entitlement is created.
const (
	// MaxContractBackdate is how far in the past a contract may start, for
	// one signed before it was entered. Backdated charges are billed
	// straight away, so the limit keeps a mistyped year from billing
	// months that were never agreed.
	MaxContractBackdate = 31 * 24 * time.Hour

	// MaxContractLeadTime is how far ahead a contract may start.
	MaxContractLeadTime = 366 * 24 * time.Hour
)

// ValidateBillingEntitlementCreate validates a BillingEntitlement on creation.
func ValidateBillingEntitlementCreate(
	ctx context.Context,
	c client.Client,
	be *billingv1alpha1.BillingEntitlement,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, be)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, be)...)
	allErrs = append(allErrs, validateBillingContract(be.Spec.Contract, now)...)

	return allErrs
}

// ValidateBillingEntitlementUpdate validates a BillingEntitlement on update.
// Changing offerRef is allowed (staff switch); uniqueness and GA checks still apply.
// The contract can't be added, removed or moved: CEL rules on the CRD
// enforce it, and they're repeated here for defence in depth.
func ValidateBillingEntitlementUpdate(
	ctx context.Context,
	c client.Client,
	oldBE, newBE *billingv1alpha1.BillingEntitlement,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateBillingEntitlementOfferRef(ctx, c, newBE)...)
	allErrs = append(allErrs, validateSingleBillingEntitlementPerAccount(ctx, c, newBE)...)

	oldContract, newContract := oldBE.Spec.Contract, newBE.Spec.Contract
	switch {
	case (oldContract == nil) != (newContract == nil):
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "contract"),
			"contract can only be set when the entitlement is created"))
	case oldContract != nil && !oldContract.StartsAt.Equal(&newContract.StartsAt):
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "contract", "startsAt"),
			"contract.startsAt is immutable"))
	}

	return allErrs
}

// validateBillingContract checks a new contract starts at midnight UTC,
// no more than MaxContractBackdate before now and no more than
// MaxContractLeadTime after it.
func validateBillingContract(contract *billingv1alpha1.BillingContract, now time.Time) field.ErrorList {
	if contract == nil {
		return nil
	}
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "contract", "startsAt")
	startsAt := contract.StartsAt.UTC()

	if startsAt.IsZero() {
		return append(allErrs, field.Required(fldPath, "contract.startsAt is required"))
	}
	if !startsAt.Equal(startsAt.Truncate(24 * time.Hour)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be midnight UTC, since monthly charges bill on the contract's start day"))
	}
	if startsAt.Before(now.Add(-MaxContractBackdate)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be no more than 31 days in the past"))
	}
	if startsAt.After(now.Add(MaxContractLeadTime)) {
		allErrs = append(allErrs, field.Invalid(fldPath, contract.StartsAt.Format(time.RFC3339),
			"must be no more than a year ahead"))
	}
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
		return allErrs
	}

	if len(offer.Spec.ServicePricings) == 0 {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			be.Spec.OfferRef.Name,
			fmt.Sprintf("offer %q is GA but has no servicePricings snapshot yet; wait for the Offer controller to publish rates", offer.Name),
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
