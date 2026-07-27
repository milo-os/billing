// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var invoiceLog = logf.Log.WithName("invoice-webhook")

// SetupInvoiceWebhookWithManager registers the Invoice validating webhook.
//
// Validator responsibilities:
//   - Ensure period.start <= period.end.
//   - Ensure the referenced BillingAccount exists in the same namespace.
//   - Require a non-controller ownerReference to that BillingAccount.
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

	errs := validation.ValidateInvoiceCreate(ctx, r.client, invoice)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(invoice.GetObjectKind().GroupVersionKind().GroupKind(), invoice.Name, errs)
	}
	return nil, nil
}

func (r *invoiceWebhook) ValidateUpdate(ctx context.Context, oldInvoice, newInvoice *billingv1alpha1.Invoice) (admission.Warnings, error) {
	invoiceLog.Info("validating update", "name", newInvoice.Name)

	errs := validation.ValidateInvoiceUpdate(ctx, r.client, oldInvoice, newInvoice)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(newInvoice.GetObjectKind().GroupVersionKind().GroupKind(), newInvoice.Name, errs)
	}
	return nil, nil
}

func (r *invoiceWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.Invoice) (admission.Warnings, error) {
	return nil, nil
}
