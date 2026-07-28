// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateInvoicePeriod checks that period.start <= period.end.
func ValidateInvoicePeriod(period billingv1alpha1.InvoicePeriod, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if period.Start.After(period.End.Time) {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			period,
			"period.start must be less than or equal to period.end",
		))
	}
	return allErrs
}

// ValidateInvoiceOwnerReference requires a non-controller ownerReference
// to the referenced BillingAccount so deleting the account cascades to
// its invoice history.
func ValidateInvoiceOwnerReference(invoice *billingv1alpha1.Invoice, account *billingv1alpha1.BillingAccount) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("metadata", "ownerReferences")

	expectedAPIVersion := billingv1alpha1.GroupVersion.String()
	for i := range invoice.OwnerReferences {
		ref := invoice.OwnerReferences[i]
		if ref.Kind != "BillingAccount" {
			continue
		}
		if ref.APIVersion != expectedAPIVersion {
			continue
		}
		if ref.Name != account.Name {
			continue
		}
		if ref.UID != account.UID {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Index(i).Child("uid"),
				ref.UID,
				fmt.Sprintf("ownerReference uid must match BillingAccount %q", account.Name),
			))
			return allErrs
		}
		if ref.Controller != nil && *ref.Controller {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Index(i).Child("controller"),
				true,
				"ownerReference to BillingAccount must have controller=false because an account accumulates many invoices",
			))
			return allErrs
		}
		return nil
	}

	allErrs = append(allErrs, field.Required(
		fldPath,
		fmt.Sprintf("must include a non-controller ownerReference to BillingAccount %q (%s)", account.Name, expectedAPIVersion),
	))
	return allErrs
}

// ValidateInvoiceBillingAccountRef checks that billingAccountRef points at
// an existing BillingAccount in the same namespace and returns that
// account for further validation (ownerReferences).
func ValidateInvoiceBillingAccountRef(
	ctx context.Context,
	c client.Reader,
	invoice *billingv1alpha1.Invoice,
) (*billingv1alpha1.BillingAccount, field.ErrorList) {
	var allErrs field.ErrorList
	fld := field.NewPath("spec", "billingAccountRef", "name")
	if invoice.Spec.BillingAccountRef.Name == "" {
		allErrs = append(allErrs, field.Required(fld, "billingAccountRef.name is required"))
		return nil, allErrs
	}

	var ba billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: invoice.Namespace, Name: invoice.Spec.BillingAccountRef.Name}
	if err := c.Get(ctx, key, &ba); err != nil {
		if errors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(fld, invoice.Spec.BillingAccountRef.Name))
		} else {
			allErrs = append(allErrs, field.InternalError(fld, fmt.Errorf("reading billing account: %w", err)))
		}
		return nil, allErrs
	}
	return &ba, nil
}

// ValidateInvoiceCreate validates an Invoice on creation.
func ValidateInvoiceCreate(ctx context.Context, c client.Reader, invoice *billingv1alpha1.Invoice) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = append(allErrs, ValidateInvoicePeriod(invoice.Spec.Period, field.NewPath("spec", "period"))...)

	ba, errs := ValidateInvoiceBillingAccountRef(ctx, c, invoice)
	allErrs = append(allErrs, errs...)
	if ba != nil {
		allErrs = append(allErrs, ValidateInvoiceOwnerReference(invoice, ba)...)
	}
	return allErrs
}

// ValidateInvoiceUpdate validates an Invoice on update.
func ValidateInvoiceUpdate(ctx context.Context, c client.Reader, oldInvoice, newInvoice *billingv1alpha1.Invoice) field.ErrorList {
	var allErrs field.ErrorList

	if oldInvoice.Spec.BillingAccountRef.Name != newInvoice.Spec.BillingAccountRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "billingAccountRef"),
			"billingAccountRef is immutable",
		))
	}
	if !oldInvoice.Spec.Period.Start.Equal(&newInvoice.Spec.Period.Start) ||
		!oldInvoice.Spec.Period.End.Equal(&newInvoice.Spec.Period.End) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "period"),
			"period is immutable",
		))
	}

	allErrs = append(allErrs, ValidateInvoicePeriod(newInvoice.Spec.Period, field.NewPath("spec", "period"))...)

	ba, errs := ValidateInvoiceBillingAccountRef(ctx, c, newInvoice)
	allErrs = append(allErrs, errs...)
	if ba != nil {
		allErrs = append(allErrs, ValidateInvoiceOwnerReference(newInvoice, ba)...)
	}
	return allErrs
}
