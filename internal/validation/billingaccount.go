// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"regexp"

	"k8s.io/apimachinery/pkg/util/validation/field"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// emailRegex is a basic email format validation pattern.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ValidateBillingAccountCreate validates a BillingAccount on creation.
func ValidateBillingAccountCreate(account *billingv1alpha1.BillingAccount) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validatePaymentProfile(account.Spec.PaymentProfile, field.NewPath("spec", "paymentProfile"))...)
	allErrs = append(allErrs, validateContactInfo(account.Spec.ContactInfo, field.NewPath("spec", "contactInfo"))...)

	return allErrs
}

// ValidateBillingAccountUpdate validates a BillingAccount on update.
func ValidateBillingAccountUpdate(oldAccount, newAccount *billingv1alpha1.BillingAccount) field.ErrorList {
	var allErrs field.ErrorList

	// CurrencyCode is immutable once past Provisioning phase
	if oldAccount.Status.Phase != "" &&
		oldAccount.Status.Phase != billingv1alpha1.BillingAccountPhaseProvisioning &&
		oldAccount.Spec.CurrencyCode != newAccount.Spec.CurrencyCode {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "currencyCode"),
			"currencyCode is immutable once the account has been activated",
		))
	}

	allErrs = append(allErrs, validatePaymentProfile(newAccount.Spec.PaymentProfile, field.NewPath("spec", "paymentProfile"))...)
	allErrs = append(allErrs, validateContactInfo(newAccount.Spec.ContactInfo, field.NewPath("spec", "contactInfo"))...)

	return allErrs
}

func validatePaymentProfile(profile *billingv1alpha1.PaymentProfileRef, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if profile == nil {
		return allErrs
	}

	if profile.Type == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("type"), "payment profile type is required when profile is set"))
	}
	if profile.ExternalID == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("externalID"), "external ID is required when profile is set"))
	}

	return allErrs
}

func validateContactInfo(contact *billingv1alpha1.BillingContactInfo, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if contact == nil {
		return allErrs
	}

	if contact.Email == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("email"), "email is required when contact info is set"))
	} else if !emailRegex.MatchString(contact.Email) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("email"), contact.Email, "must be a valid email address"))
	}

	return allErrs
}
