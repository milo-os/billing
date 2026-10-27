// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var amountPattern = regexp.MustCompile(`^([1-9][0-9]*(\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)$`)

// ValidateCreditGrantCreate validates a CreditGrant on creation.
func ValidateCreditGrantCreate(
	ctx context.Context,
	c client.Reader,
	grant *billingv1alpha1.CreditGrant,
) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateCreditGrantBillingAccountRef(ctx, c, grant)...)
	allErrs = append(allErrs, validateCreditGrantSpec(&grant.Spec, field.NewPath("spec"))...)

	if grant.Spec.Voided {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "voided"),
			"a new credit grant cannot be created as voided",
		))
	}

	return allErrs
}

// ValidateCreditGrantUpdate validates a CreditGrant on update.
// Most fields are immutable, enforced by CEL and repeated here for defense in depth.
func ValidateCreditGrantUpdate(
	_ context.Context,
	_ client.Reader,
	oldGrant, newGrant *billingv1alpha1.CreditGrant,
) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	if oldGrant.Spec.BillingAccountRef.Name != newGrant.Spec.BillingAccountRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("billingAccountRef"),
			"billingAccountRef is immutable",
		))
	}
	if oldGrant.Spec.Amount != newGrant.Spec.Amount {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("amount"),
			"amount is immutable",
		))
	}
	if oldGrant.Spec.Currency != newGrant.Spec.Currency {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("currency"),
			"currency is immutable",
		))
	}
	if oldGrant.Spec.Reason != newGrant.Spec.Reason {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("reason"),
			"reason is immutable",
		))
	}
	if oldGrant.Spec.Description != newGrant.Spec.Description {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("description"),
			"description is immutable",
		))
	}
	if !durationEqual(oldGrant.Spec.ExpiresAfter, newGrant.Spec.ExpiresAfter) {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("expiresAfter"),
			"expiresAfter is immutable",
		))
	}

	if oldGrant.Spec.Voided && !newGrant.Spec.Voided {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("voided"),
			"a voided credit grant cannot be unvoided",
		))
	}

	return allErrs
}

// ValidateCreditGrantDelete validates a CreditGrant on deletion.
// Credit grants cannot be deleted so that an immutable audit trail is preserved.
// To revoke or cancel a credit grant, void it by setting spec.voided: true.
//
// Deletion is only permitted when the parent BillingAccount is being deleted
// or is already gone, allowing Kubernetes garbage collection to clean up the account.
func ValidateCreditGrantDelete(
	ctx context.Context,
	c client.Reader,
	grant *billingv1alpha1.CreditGrant,
) field.ErrorList {
	if c != nil && grant.Spec.BillingAccountRef.Name != "" {
		var account billingv1alpha1.BillingAccount
		key := client.ObjectKey{Namespace: grant.Namespace, Name: grant.Spec.BillingAccountRef.Name}
		if err := c.Get(ctx, key, &account); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return field.ErrorList{
				field.InternalError(
					field.NewPath("spec", "billingAccountRef", "name"),
					fmt.Errorf("reading billing account: %w", err),
				),
			}
		} else if !account.DeletionTimestamp.IsZero() {
			return nil
		}
	}

	return field.ErrorList{
		field.Forbidden(
			field.NewPath("metadata"),
			"credit grants cannot be deleted; void the grant instead by setting spec.voided: true",
		),
	}
}

func validateCreditGrantBillingAccountRef(
	ctx context.Context,
	c client.Reader,
	grant *billingv1alpha1.CreditGrant,
) field.ErrorList {
	_, errs := getReferencedBillingAccount(ctx, c, grant.Namespace, grant.Spec.BillingAccountRef.Name)
	return errs
}

func validateCreditGrantSpec(spec *billingv1alpha1.CreditGrantSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if spec.BillingAccountRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("billingAccountRef", "name"),
			"billingAccountRef.name is required",
		))
	}

	if spec.Amount == "" {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("amount"),
			"amount is required",
		))
	} else if !amountPattern.MatchString(spec.Amount) {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("amount"),
			spec.Amount,
			"amount must be a positive decimal amount",
		))
	} else {
		q, err := resource.ParseQuantity(spec.Amount)
		if err != nil || q.Sign() <= 0 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("amount"),
				spec.Amount,
				"amount must be a positive decimal amount",
			))
		}
	}

	if spec.Currency != "USD" {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("currency"),
			spec.Currency,
			"currency must be USD",
		))
	}

	switch spec.Reason {
	case billingv1alpha1.CreditGrantReasonPromotional,
		billingv1alpha1.CreditGrantReasonSponsorship,
		billingv1alpha1.CreditGrantReasonGoodwill:
		// Valid
	default:
		allErrs = append(allErrs, field.NotSupported(
			fldPath.Child("reason"),
			spec.Reason,
			[]string{
				string(billingv1alpha1.CreditGrantReasonPromotional),
				string(billingv1alpha1.CreditGrantReasonSponsorship),
				string(billingv1alpha1.CreditGrantReasonGoodwill),
			},
		))
	}

	if spec.Description == "" {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("description"),
			"description is required",
		))
	}

	if spec.ExpiresAfter != nil && spec.ExpiresAfter.Duration <= 0 {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("expiresAfter"),
			spec.ExpiresAfter.Duration.String(),
			"expiresAfter must be greater than zero",
		))
	}

	return allErrs
}

func durationEqual(a, b *metav1.Duration) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Duration == b.Duration
}
