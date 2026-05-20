// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// emailRegex is a basic email format validation pattern.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ValidateBillingAccountCreate validates a BillingAccount on creation.
func ValidateBillingAccountCreate(account *billingv1alpha1.BillingAccount) field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, validateContactInfo(account.Spec.ContactInfo, field.NewPath("spec", "contactInfo"))...)
	allErrs = append(allErrs, validateBillingDetails(account.Spec.BillingDetails, field.NewPath("spec", "billingDetails"))...)

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

	allErrs = append(allErrs, validateContactInfo(newAccount.Spec.ContactInfo, field.NewPath("spec", "contactInfo"))...)
	allErrs = append(allErrs, validateBillingDetails(newAccount.Spec.BillingDetails, field.NewPath("spec", "billingDetails"))...)

	return allErrs
}

// ValidateBillingAccountDefaultPaymentMethodRef checks that the
// defaultPaymentMethodRef (when set) points at a PaymentMethod that
// exists in the same namespace and is in the Active phase. Callers pass
// a Reader so this can be invoked from the admission webhook against the
// live API server.
func ValidateBillingAccountDefaultPaymentMethodRef(ctx context.Context, c client.Reader, account *billingv1alpha1.BillingAccount) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec", "defaultPaymentMethodRef", "name")

	if account.Spec.DefaultPaymentMethodRef == nil || account.Spec.DefaultPaymentMethodRef.Name == "" {
		return allErrs
	}

	var pm billingv1alpha1.PaymentMethod
	key := types.NamespacedName{Namespace: account.Namespace, Name: account.Spec.DefaultPaymentMethodRef.Name}
	if err := c.Get(ctx, key, &pm); err != nil {
		if errors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(fldPath, account.Spec.DefaultPaymentMethodRef.Name))
		} else {
			allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("reading payment method: %w", err)))
		}
		return allErrs
	}

	if pm.Status.Phase != billingv1alpha1.PaymentMethodPhaseActive {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			account.Spec.DefaultPaymentMethodRef.Name,
			fmt.Sprintf("payment method is in %s phase; only Active payment methods may be designated as default", pm.Status.Phase),
		))
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

// taxIDTypePattern enforces the shape of Stripe's tax_id_data.type
// vocabulary (e.g. "gb_vat", "eu_vat", "us_ein") without locking the
// API into the exact set of types upstream supports today.
var taxIDTypePattern = regexp.MustCompile(`^[a-z]{2}_[a-z][a-z_]*$`)

// iso3166Alpha2Pattern enforces a two-letter uppercase country code.
var iso3166Alpha2Pattern = regexp.MustCompile(`^[A-Z]{2}$`)

func validateBillingDetails(details *billingv1alpha1.BillingDetails, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if details == nil {
		return allErrs
	}

	if details.InvoiceEmail != "" && !emailRegex.MatchString(details.InvoiceEmail) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("invoiceEmail"), details.InvoiceEmail, "must be a valid email address"))
	}

	if details.Address != nil {
		addrPath := fldPath.Child("address")
		if details.Address.Country == "" {
			allErrs = append(allErrs, field.Required(addrPath.Child("country"), "country is required when address is set"))
		} else if !iso3166Alpha2Pattern.MatchString(details.Address.Country) {
			allErrs = append(allErrs, field.Invalid(addrPath.Child("country"), details.Address.Country, "must be an ISO 3166-1 alpha-2 country code"))
		}
	}

	taxPath := fldPath.Child("taxIds")
	seenTypes := make(map[string]struct{}, len(details.TaxIDs))
	for i, tid := range details.TaxIDs {
		idxPath := taxPath.Index(i)
		if tid.Type == "" {
			allErrs = append(allErrs, field.Required(idxPath.Child("type"), "tax ID type is required"))
		} else if !taxIDTypePattern.MatchString(tid.Type) {
			allErrs = append(allErrs, field.Invalid(idxPath.Child("type"), tid.Type, `must match Stripe tax_id_data.type vocabulary (e.g. "gb_vat", "eu_vat", "us_ein")`))
		}
		if tid.Value == "" {
			allErrs = append(allErrs, field.Required(idxPath.Child("value"), "tax ID value is required"))
		}
		if _, dup := seenTypes[tid.Type]; dup && tid.Type != "" {
			allErrs = append(allErrs, field.Duplicate(idxPath.Child("type"), tid.Type))
		}
		seenTypes[tid.Type] = struct{}{}
	}

	return allErrs
}
