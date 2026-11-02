// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ValidateInvoiceRequestCreate validates an InvoiceRequest on creation: the
// billing account it names exists in the same namespace and isn't being
// deleted.
func ValidateInvoiceRequestCreate(
	ctx context.Context,
	c client.Reader,
	req *billingv1alpha1.InvoiceRequest,
) field.ErrorList {
	_, errs := getReferencedBillingAccount(ctx, c, req.Namespace, req.Spec.BillingAccountRef.Name)
	return errs
}

// ValidateInvoiceRequestUpdate validates an InvoiceRequest on update. The
// spec is immutable: a CEL rule on the CRD enforces it, and it's repeated
// here for defence in depth.
func ValidateInvoiceRequestUpdate(oldReq, newReq *billingv1alpha1.InvoiceRequest) field.ErrorList {
	if oldReq.Spec != newReq.Spec {
		return field.ErrorList{field.Forbidden(field.NewPath("spec"), "spec is immutable")}
	}
	return nil
}
