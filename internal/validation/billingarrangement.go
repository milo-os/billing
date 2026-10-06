// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"net/mail"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateBillingArrangementCreate validates a BillingArrangement on
// creation. now stands in for spec.startsAt when the defaulting webhook
// hasn't set it.
//
// c should be an uncached reader: the overlap check must see
// arrangements admitted moments ago, which a cache may not have yet.
func ValidateBillingArrangementCreate(
	ctx context.Context,
	c client.Reader,
	arr *billingv1alpha1.BillingArrangement,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList

	start := ArrangementStart(arr, now)
	allErrs = append(allErrs, validateArrangementBillingAccountRef(ctx, c, arr)...)
	allErrs = append(allErrs, validateBillingArrangementSpec(arr, start, false, now)...)
	allErrs = append(allErrs, validateNoOverlappingArrangement(ctx, c, arr, start)...)

	return allErrs
}

// ValidateBillingArrangementUpdate validates a BillingArrangement on
// update. billingAccountRef and type immutability are enforced by CEL
// rules on the CRD and repeated here for defence in depth.
//
// Two allowances keep staff able to end terms, which keeps the record
// rather than deleting it:
//   - endsAt may be at or before startsAt when it isn't in the future,
//     so scheduled terms can be withdrawn before they start.
//   - an update that only narrows the window skips the overlap check, so
//     arrangements that already overlap (admitted before this rule, or in
//     a race) can still be ended or edited.
func ValidateBillingArrangementUpdate(
	ctx context.Context,
	c client.Reader,
	oldArr, newArr *billingv1alpha1.BillingArrangement,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList

	if oldArr.Spec.BillingAccountRef.Name != newArr.Spec.BillingAccountRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "billingAccountRef"), "billingAccountRef is immutable"))
	}
	if oldArr.Spec.Type != newArr.Spec.Type {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "type"), "type is immutable; end this arrangement and create a new one"))
	}

	start := ArrangementStart(newArr, newArr.CreationTimestamp.Time)
	allErrs = append(allErrs, validateBillingArrangementSpec(newArr, start, true, now)...)

	oldStart := ArrangementStart(oldArr, oldArr.CreationTimestamp.Time)
	if !windowWithin(start, newArr.Spec.EndsAt, oldStart, oldArr.Spec.EndsAt) {
		allErrs = append(allErrs, validateNoOverlappingArrangement(ctx, c, newArr, start)...)
	}

	return allErrs
}

// ArrangementStart is when an arrangement takes effect: spec.startsAt,
// or fallback when it's unset (the creation time once stored).
func ArrangementStart(arr *billingv1alpha1.BillingArrangement, fallback time.Time) time.Time {
	if arr.Spec.StartsAt != nil {
		return arr.Spec.StartsAt.Time
	}
	return fallback
}

// ArrangementPhaseAt computes an arrangement's phase at the given time.
// The window is half-open: active from startsAt up to, not including,
// endsAt. An endsAt at or before startsAt (terms withdrawn before they
// started) is Ended from endsAt onwards.
func ArrangementPhaseAt(arr *billingv1alpha1.BillingArrangement, now time.Time) billingv1alpha1.BillingArrangementPhase {
	switch {
	case arr.Spec.EndsAt != nil && !now.Before(arr.Spec.EndsAt.Time):
		return billingv1alpha1.BillingArrangementPhaseEnded
	case now.Before(ArrangementStart(arr, arr.CreationTimestamp.Time)):
		return billingv1alpha1.BillingArrangementPhaseScheduled
	default:
		return billingv1alpha1.BillingArrangementPhaseActive
	}
}

func validateBillingArrangementSpec(arr *billingv1alpha1.BillingArrangement, start time.Time, allowEnding bool, now time.Time) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	if end := arr.Spec.EndsAt; end != nil && !end.After(start) {
		withdrawing := allowEnding && !end.After(now)
		if !withdrawing {
			allErrs = append(allErrs, field.Invalid(
				specPath.Child("endsAt"), end.UTC().Format(time.RFC3339),
				"endsAt must be after startsAt; to withdraw terms before they start, set endsAt to the current time"))
		}
	}

	if arr.Spec.Type != billingv1alpha1.BillingArrangementTypeInvoice && arr.Spec.Invoice != nil {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("invoice"), "invoice may only be set when type is Invoice"))
	}

	if inv := arr.Spec.Invoice; inv != nil {
		invPath := specPath.Child("invoice")
		if q, err := resource.ParseQuantity(inv.CreditLimit); err != nil || q.Sign() <= 0 {
			allErrs = append(allErrs, field.Invalid(
				invPath.Child("creditLimit"), inv.CreditLimit, "creditLimit must be a positive decimal amount"))
		}
		if inv.AccountsPayableEmail != "" {
			if addr, err := mail.ParseAddress(inv.AccountsPayableEmail); err != nil || addr.Address != inv.AccountsPayableEmail {
				allErrs = append(allErrs, field.Invalid(
					invPath.Child("accountsPayableEmail"), inv.AccountsPayableEmail, "must be a plain email address"))
			}
		}
	}

	return allErrs
}

func validateArrangementBillingAccountRef(
	ctx context.Context,
	c client.Reader,
	arr *billingv1alpha1.BillingArrangement,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "billingAccountRef", "name")

	if arr.Spec.BillingAccountRef.Name == "" {
		return append(allErrs, field.Required(fldPath, "billingAccountRef.name is required"))
	}

	var account billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: arr.Namespace, Name: arr.Spec.BillingAccountRef.Name}
	if err := c.Get(ctx, key, &account); err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(fldPath, arr.Spec.BillingAccountRef.Name))
		} else {
			allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("reading billing account: %w", err)))
		}
		return allErrs
	}
	if !account.DeletionTimestamp.IsZero() {
		allErrs = append(allErrs, field.Forbidden(fldPath,
			fmt.Sprintf("billing account %q is being deleted", account.Name)))
	}
	return allErrs
}

// validateNoOverlappingArrangement rejects an arrangement whose window
// overlaps another non-deleting arrangement for the same billing
// account, so at most one set of terms applies at any time.
//
// This lists the namespace rather than using the billingAccountRef
// field index, because the index lives in the cache and this must read
// through to the API server. Arrangements per organization are few.
func validateNoOverlappingArrangement(
	ctx context.Context,
	c client.Reader,
	arr *billingv1alpha1.BillingArrangement,
	start time.Time,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "startsAt")

	if arr.Spec.BillingAccountRef.Name == "" || windowEmpty(start, arr.Spec.EndsAt) {
		return allErrs
	}

	var list billingv1alpha1.BillingArrangementList
	if err := c.List(ctx, &list, client.InNamespace(arr.Namespace)); err != nil {
		return append(allErrs, field.InternalError(fldPath, fmt.Errorf("listing billing arrangements: %w", err)))
	}

	for i := range list.Items {
		other := &list.Items[i]
		if other.UID == arr.UID || other.Name == arr.Name {
			continue
		}
		if !other.DeletionTimestamp.IsZero() {
			continue
		}
		if other.Spec.BillingAccountRef.Name != arr.Spec.BillingAccountRef.Name {
			continue
		}
		otherStart := ArrangementStart(other, other.CreationTimestamp.Time)
		if windowEmpty(otherStart, other.Spec.EndsAt) {
			continue
		}
		if windowsOverlap(start, arr.Spec.EndsAt, otherStart, other.Spec.EndsAt) {
			allErrs = append(allErrs, field.Forbidden(fldPath,
				fmt.Sprintf("overlaps arrangement %q for billing account %q; end it before this one starts",
					other.Name, arr.Spec.BillingAccountRef.Name)))
			break
		}
	}
	return allErrs
}

// windowsOverlap reports whether two half-open windows [start, end)
// intersect. A nil end means the window never closes.
func windowsOverlap(aStart time.Time, aEnd *metav1.Time, bStart time.Time, bEnd *metav1.Time) bool {
	return startsBeforeEnd(aStart, bEnd) && startsBeforeEnd(bStart, aEnd)
}

// windowWithin reports whether window a lies inside window b.
func windowWithin(aStart time.Time, aEnd *metav1.Time, bStart time.Time, bEnd *metav1.Time) bool {
	if aStart.Before(bStart) {
		return false
	}
	return bEnd == nil || (aEnd != nil && !aEnd.After(bEnd.Time))
}

// windowEmpty reports whether a window never applies (terms withdrawn
// before they started).
func windowEmpty(start time.Time, end *metav1.Time) bool {
	return end != nil && !start.Before(end.Time)
}

func startsBeforeEnd(start time.Time, end *metav1.Time) bool {
	return end == nil || start.Before(end.Time)
}
