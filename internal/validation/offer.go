// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// OfferUpdateOptions carries admission context for Offer update validation.
type OfferUpdateOptions struct {
	// AllowSnapshotWrite permits the one-time empty→populated servicePricings
	// fill. Only the billing operator service account should set this.
	AllowSnapshotWrite bool
}

// ValidateOfferCreate validates an Offer on creation.
// servicePricings must be empty: the Offer reconciler owns the snapshot.
func ValidateOfferCreate(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateOfferChargeTypes(offer)...)
	allErrs = append(allErrs, validateOfferClientSnapshotEmpty(offer)...)
	allErrs = append(allErrs, validateOfferGAHasRefs(offer)...)

	return allErrs
}

// ValidateOfferUpdate validates an Offer on update.
func ValidateOfferUpdate(oldOffer, newOffer *billingv1alpha1.Offer, opts OfferUpdateOptions) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateOfferChargeTypes(newOffer)...)
	allErrs = append(allErrs, validateOfferChargeTypesCoverSnapshot(newOffer)...)
	allErrs = append(allErrs, validateOfferGAHasRefs(newOffer)...)
	allErrs = append(allErrs, validateOfferGAImmutability(oldOffer, newOffer, opts)...)

	return allErrs
}

// OfferIsAssignable reports whether an Offer may be referenced by a
// BillingEntitlement: GA with a non-empty controller snapshot.
func OfferIsAssignable(offer *billingv1alpha1.Offer) bool {
	return offer != nil &&
		offer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageGA &&
		len(offer.Spec.ServicePricings) > 0
}

func validateOfferChargeTypes(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "chargeTypes")

	if len(offer.Spec.ChargeTypes) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "chargeTypes must be non-empty"))
	}
	return allErrs
}

func validateOfferClientSnapshotEmpty(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList
	if len(offer.Spec.ServicePricings) > 0 {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "servicePricings"),
			"servicePricings is owned by the Offer controller and must not be set by clients",
		))
	}
	return allErrs
}

func validateOfferGAHasRefs(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList
	if offer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return allErrs
	}
	// Once the controller has written the snapshot, refs may still be present
	// for audit; either refs or an existing snapshot is required to publish.
	if len(offer.Spec.ServicePricingRefs) == 0 && len(offer.Spec.ServicePricings) == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "servicePricingRefs"),
			"GA Offer requires servicePricingRefs so the controller can snapshot rates",
		))
	}
	return allErrs
}

// validateOfferChargeTypesCoverSnapshot ensures chargeTypes covers every
// charge type present in the snapshotted servicePricings.
func validateOfferChargeTypesCoverSnapshot(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList
	if len(offer.Spec.ServicePricings) == 0 {
		return allErrs
	}

	declared := make(map[billingv1alpha1.ChargeType]struct{}, len(offer.Spec.ChargeTypes))
	for _, ct := range offer.Spec.ChargeTypes {
		declared[ct] = struct{}{}
	}

	fldPath := field.NewPath("spec", "chargeTypes")
	seenMissing := make(map[billingv1alpha1.ChargeType]struct{})
	for i, snap := range offer.Spec.ServicePricings {
		ct := snap.Spec.ChargeType
		if ct == "" {
			continue
		}
		if _, ok := declared[ct]; ok {
			continue
		}
		if _, already := seenMissing[ct]; already {
			continue
		}
		seenMissing[ct] = struct{}{}
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			offer.Spec.ChargeTypes,
			fmt.Sprintf("chargeTypes must include %q present in servicePricings[%d] (%s)", ct, i, snap.Name),
		))
	}

	return allErrs
}

// validateOfferGAImmutability rejects mutations once an Offer is GA, except
// for the kubernetes.io/display-name annotation. Draft→GA (without a client
// snapshot) and the controller's one-time snapshot population are permitted.
func validateOfferGAImmutability(oldOffer, newOffer *billingv1alpha1.Offer, opts OfferUpdateOptions) field.ErrorList {
	var allErrs field.ErrorList

	if oldOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA &&
		newOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return allErrs
	}

	if isDraftToGATransition(oldOffer, newOffer) {
		if len(newOffer.Spec.ServicePricings) > 0 {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "servicePricings"),
				"servicePricings must remain empty on Draft→GA; the Offer controller writes the snapshot",
			))
		}
		return allErrs
	}

	if isControllerSnapshotFill(oldOffer, newOffer) {
		if !opts.AllowSnapshotWrite {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "servicePricings"),
				"servicePricings may only be written by the billing Offer controller",
			))
		}
		return allErrs
	}

	oldCmp := oldOffer.DeepCopy()
	newCmp := newOffer.DeepCopy()
	normalizeOfferForImmutabilityCompare(oldCmp, newCmp)

	if !apiequality.Semantic.DeepEqual(oldCmp.Spec, newCmp.Spec) ||
		!apiequality.Semantic.DeepEqual(oldCmp.ObjectMeta, newCmp.ObjectMeta) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec"),
			"Offer is immutable once launchStage is GA except for the kubernetes.io/display-name annotation",
		))
	}

	return allErrs
}

// isDraftToGATransition reports Draft→GA with all other spec fields equal,
// ignoring servicePricings (callers must separately require it empty on new).
func isDraftToGATransition(oldOffer, newOffer *billingv1alpha1.Offer) bool {
	if oldOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageDraft ||
		newOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return false
	}
	oldSpec := oldOffer.Spec.DeepCopy()
	newSpec := newOffer.Spec.DeepCopy()
	oldSpec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
	oldSpec.ServicePricings = newSpec.ServicePricings
	return apiequality.Semantic.DeepEqual(oldSpec, newSpec)
}

// IsControllerSnapshotFill reports GA→GA with empty→non-empty servicePricings
// and no other spec changes.
func IsControllerSnapshotFill(oldOffer, newOffer *billingv1alpha1.Offer) bool {
	return isControllerSnapshotFill(oldOffer, newOffer)
}

func isControllerSnapshotFill(oldOffer, newOffer *billingv1alpha1.Offer) bool {
	if oldOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA ||
		newOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return false
	}
	if len(oldOffer.Spec.ServicePricings) != 0 || len(newOffer.Spec.ServicePricings) == 0 {
		return false
	}
	oldSpec := oldOffer.Spec.DeepCopy()
	newSpec := newOffer.Spec.DeepCopy()
	oldSpec.ServicePricings = newSpec.ServicePricings
	return apiequality.Semantic.DeepEqual(oldSpec, newSpec)
}

func normalizeOfferForImmutabilityCompare(oldOffer, newOffer *billingv1alpha1.Offer) {
	equalizeDisplayNameAnnotation(oldOffer, newOffer)
	clearOfferMetadataNoise(oldOffer)
	clearOfferMetadataNoise(newOffer)
}

func equalizeDisplayNameAnnotation(oldOffer, newOffer *billingv1alpha1.Offer) {
	newName := ""
	if newOffer.Annotations != nil {
		newName = newOffer.Annotations[billingv1alpha1.DisplayNameAnnotation]
	}
	if oldOffer.Annotations == nil {
		oldOffer.Annotations = map[string]string{}
	}
	if newName == "" {
		delete(oldOffer.Annotations, billingv1alpha1.DisplayNameAnnotation)
		if newOffer.Annotations != nil {
			delete(newOffer.Annotations, billingv1alpha1.DisplayNameAnnotation)
		}
		if len(oldOffer.Annotations) == 0 {
			oldOffer.Annotations = nil
		}
		if newOffer.Annotations != nil && len(newOffer.Annotations) == 0 {
			newOffer.Annotations = nil
		}
		return
	}
	oldOffer.Annotations[billingv1alpha1.DisplayNameAnnotation] = newName
}

func clearOfferMetadataNoise(offer *billingv1alpha1.Offer) {
	offer.ResourceVersion = ""
	offer.Generation = 0
	offer.ManagedFields = nil
	offer.UID = ""
	offer.CreationTimestamp = metav1.Time{}
	offer.DeletionTimestamp = nil
	offer.DeletionGracePeriodSeconds = nil
	offer.OwnerReferences = nil
	offer.Finalizers = nil
	offer.Status = billingv1alpha1.OfferStatus{}
	offer.TypeMeta = metav1.TypeMeta{}
}
