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

// ValidateChargeDraft validates a ChargeDraft on create and on every
// change: its billing account takes charges, and each line would make a
// valid Charge once billing fills in what it leaves out. Problems with a
// line are reported at spec.lines[i], so staff see them when they save,
// not when they send.
func ValidateChargeDraft(ctx context.Context, c client.Reader, draft *billingv1alpha1.ChargeDraft, now time.Time) field.ErrorList {
	specPath := field.NewPath("spec")
	account, allErrs := validateChargeAccount(ctx, c, draft.Namespace, draft.Spec.BillingAccountRef.Name,
		specPath.Child("billingAccountRef", "name"))
	if len(draft.Spec.Lines) == 0 {
		allErrs = append(allErrs, field.Required(specPath.Child("lines"), "a draft needs at least one line"))
	}
	invoice := draft.Spec.Invoice
	if invoice == "" {
		invoice = billingv1alpha1.ChargeInvoiceNext
	}
	for i := range draft.Spec.Lines {
		line := draft.Spec.Lines[i].DeepCopy()
		spec := line.ChargeSpec(draft.Spec.BillingAccountRef.Name, invoice)
		ApplyChargeDefaults(&spec, chargePricing(ctx, c, spec.ServicePricingRef), account, now)
		charge := &billingv1alpha1.Charge{Spec: spec}
		charge.Namespace = draft.Namespace
		allErrs = append(allErrs, validateChargeFields(ctx, c, charge, account, now, specPath.Child("lines").Index(i))...)
	}
	return allErrs
}

// chargePricing reads a charge's catalog item, or nil without one or when
// it can't be read; validation then reports why.
func chargePricing(ctx context.Context, c client.Reader, ref *billingv1alpha1.ChargeServicePricingRef) *billingv1alpha1.ServicePricing {
	if ref == nil || ref.Name == "" || c == nil {
		return nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultPricingNamespace
	}
	var pricing billingv1alpha1.ServicePricing
	if c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &pricing) != nil {
		return nil
	}
	return &pricing
}

// validateInvoiceRequestChargeDraft checks the ChargeDraft an
// InvoiceRequest previews: it exists, in the request's namespace, for the
// request's billing account.
func validateInvoiceRequestChargeDraft(ctx context.Context, c client.Reader, req *billingv1alpha1.InvoiceRequest) field.ErrorList {
	ref := req.Spec.ChargeDraftRef
	if ref == nil {
		return nil
	}
	fldPath := field.NewPath("spec", "chargeDraftRef", "name")
	var draft billingv1alpha1.ChargeDraft
	if err := c.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: ref.Name}, &draft); err != nil {
		if apierrors.IsNotFound(err) {
			return field.ErrorList{field.NotFound(fldPath, ref.Name)}
		}
		return field.ErrorList{field.InternalError(fldPath, fmt.Errorf("reading charge draft: %w", err))}
	}
	if draft.Spec.BillingAccountRef.Name != req.Spec.BillingAccountRef.Name {
		return field.ErrorList{field.Invalid(fldPath, ref.Name, fmt.Sprintf(
			"charge draft %q is for billing account %q, not %q", ref.Name, draft.Spec.BillingAccountRef.Name, req.Spec.BillingAccountRef.Name))}
	}
	return nil
}
