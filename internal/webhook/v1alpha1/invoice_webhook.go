// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var invoiceLog = logf.Log.WithName("invoice-webhook")

// SetupInvoiceWebhookWithManager registers the Invoice validating webhook.
//
// Validator responsibilities:
//   - Ensure the referenced BillingAccount exists in the same namespace.
//   - Enforce immutability of billingAccountRef and period on update
//     (defence-in-depth alongside the XValidation rules on the CRD).
func SetupInvoiceWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &invoiceWebhook{client: mgr.GetClient()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.Invoice{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-invoice,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=invoices,verbs=create;update,versions=v1alpha1,name=vinvoice.kb.io,admissionReviewVersions=v1

type invoiceWebhook struct {
	client client.Client
}

var _ admission.Validator[*billingv1alpha1.Invoice] = &invoiceWebhook{}

func (r *invoiceWebhook) ValidateCreate(ctx context.Context, invoice *billingv1alpha1.Invoice) (admission.Warnings, error) {
	invoiceLog.Info("validating create", "name", invoice.Name)

	errs := r.validateBillingAccountRef(ctx, invoice)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(invoice.GetObjectKind().GroupVersionKind().GroupKind(), invoice.Name, errs)
	}
	return nil, nil
}

func (r *invoiceWebhook) ValidateUpdate(ctx context.Context, oldInvoice, newInvoice *billingv1alpha1.Invoice) (admission.Warnings, error) {
	invoiceLog.Info("validating update", "name", newInvoice.Name)

	var errs field.ErrorList

	// Defence-in-depth alongside the XValidation rules on the spec.
	if oldInvoice.Spec.BillingAccountRef.Name != newInvoice.Spec.BillingAccountRef.Name {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "billingAccountRef"),
			"billingAccountRef is immutable",
		))
	}
	if !oldInvoice.Spec.Period.Start.Equal(&newInvoice.Spec.Period.Start) ||
		!oldInvoice.Spec.Period.End.Equal(&newInvoice.Spec.Period.End) {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "period"),
			"period is immutable",
		))
	}

	errs = append(errs, r.validateBillingAccountRef(ctx, newInvoice)...)

	if len(errs) > 0 {
		return nil, errors.NewInvalid(newInvoice.GetObjectKind().GroupVersionKind().GroupKind(), newInvoice.Name, errs)
	}
	return nil, nil
}

func (r *invoiceWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.Invoice) (admission.Warnings, error) {
	return nil, nil
}

func (r *invoiceWebhook) validateBillingAccountRef(ctx context.Context, invoice *billingv1alpha1.Invoice) field.ErrorList {
	var errs field.ErrorList
	fld := field.NewPath("spec", "billingAccountRef", "name")
	if invoice.Spec.BillingAccountRef.Name == "" {
		errs = append(errs, field.Required(fld, "billingAccountRef.name is required"))
		return errs
	}

	var ba billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: invoice.Namespace, Name: invoice.Spec.BillingAccountRef.Name}
	if err := r.client.Get(ctx, key, &ba); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(fld, invoice.Spec.BillingAccountRef.Name))
		} else {
			invoiceLog.Error(err, "failed to read BillingAccount during validation", "key", key)
			errs = append(errs, field.InternalError(fld, fmt.Errorf("reading billing account: %w", err)))
		}
	}
	return errs
}
