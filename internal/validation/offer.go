// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateOfferCreate validates an Offer on creation.
func ValidateOfferCreate(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateOfferChargeTypes(offer)...)
	allErrs = append(allErrs, validateOfferChargeTypesCoverSnapshot(offer)...)

	return allErrs
}

// ValidateOfferUpdate validates an Offer on update.
func ValidateOfferUpdate(oldOffer, newOffer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateOfferChargeTypes(newOffer)...)
	allErrs = append(allErrs, validateOfferChargeTypesCoverSnapshot(newOffer)...)
	allErrs = append(allErrs, validateOfferGAImmutability(oldOffer, newOffer)...)

	return allErrs
}

func validateOfferChargeTypes(offer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "chargeTypes")

	if len(offer.Spec.ChargeTypes) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "chargeTypes must be non-empty"))
	}
	return allErrs
}

// validateOfferChargeTypesCoverSnapshot ensures chargeTypes covers every
// charge type present in the snapshotted servicePricings. Live refs are
// checked at controller time (admission cannot resolve them without a client
// round-trip on every draft edit).
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
// for the kubernetes.io/display-name annotation. Draft→GA and the controller's
// one-time snapshot population are permitted publish-path transitions.
func validateOfferGAImmutability(oldOffer, newOffer *billingv1alpha1.Offer) field.ErrorList {
	var allErrs field.ErrorList

	if oldOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA &&
		newOffer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return allErrs
	}

	if isOfferPublishTransition(oldOffer, newOffer) {
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

// isOfferPublishTransition reports whether the update is the allowed Draft→GA
// transition and/or the controller filling an empty servicePricings snapshot.
func isOfferPublishTransition(oldOffer, newOffer *billingv1alpha1.Offer) bool {
	oldSpec := oldOffer.Spec.DeepCopy()
	newSpec := newOffer.Spec.DeepCopy()

	switch {
	case oldOffer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageDraft &&
		newOffer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageGA:
		oldSpec.LaunchStage = billingv1alpha1.OfferLaunchStageGA
		if len(oldSpec.ServicePricings) == 0 {
			oldSpec.ServicePricings = newSpec.ServicePricings
		}
		return apiequality.Semantic.DeepEqual(oldSpec, newSpec)

	case oldOffer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageGA &&
		newOffer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageGA &&
		len(oldOffer.Spec.ServicePricings) == 0 &&
		len(newOffer.Spec.ServicePricings) > 0:
		oldSpec.ServicePricings = newSpec.ServicePricings
		return apiequality.Semantic.DeepEqual(oldSpec, newSpec)
	}

	return false
}

// normalizeOfferForImmutabilityCompare equalizes display-name and strips
// admission noise so Semantic.DeepEqual can compare user-visible identity.
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
	// Status is not part of the immutability contract.
	offer.Status = billingv1alpha1.OfferStatus{}
	offer.TypeMeta = metav1.TypeMeta{}
}
