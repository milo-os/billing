// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var invoiceRequestLog = logf.Log.WithName("invoicerequest-webhook")

// SetupInvoiceRequestWebhookWithManager registers the InvoiceRequest
// defaulting and validating webhooks.
//
// Defaulter responsibilities:
//   - Add an owner reference to the BillingAccount on create, so requests
//     are garbage collected with their account.
//
// Validator responsibilities:
//   - Ensure the referenced BillingAccount exists in the same namespace
//     and isn't being deleted.
//   - Ensure the spec never changes.
func SetupInvoiceRequestWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &invoiceRequestWebhook{reader: mgr.GetAPIReader()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.InvoiceRequest{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-invoicerequest,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=invoicerequests,verbs=create,versions=v1alpha1,name=minvoicerequest.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-invoicerequest,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=invoicerequests,verbs=create;update,versions=v1alpha1,name=vinvoicerequest.kb.io,admissionReviewVersions=v1

type invoiceRequestWebhook struct {
	reader client.Reader
}

var (
	_ admission.Defaulter[*billingv1alpha1.InvoiceRequest] = &invoiceRequestWebhook{}
	_ admission.Validator[*billingv1alpha1.InvoiceRequest] = &invoiceRequestWebhook{}
)

func (r *invoiceRequestWebhook) Default(ctx context.Context, req *billingv1alpha1.InvoiceRequest) error {
	if admReq, err := admission.RequestFromContext(ctx); err == nil && admReq.Operation != admissionv1.Create {
		return nil
	}
	// A missing or unreadable account is left for ValidateCreate to report.
	var account billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Spec.BillingAccountRef.Name}
	if req.Spec.BillingAccountRef.Name == "" || r.reader.Get(ctx, key, &account) != nil {
		return nil
	}
	addOwnerReference(req, "BillingAccount", account.Name, account.UID)
	return nil
}

func (r *invoiceRequestWebhook) ValidateCreate(ctx context.Context, req *billingv1alpha1.InvoiceRequest) (admission.Warnings, error) {
	invoiceRequestLog.Info("validating create", "name", req.Name, "namespace", req.Namespace)

	if errs := validation.ValidateInvoiceRequestCreate(ctx, r.reader, req); len(errs) > 0 {
		return nil, errors.NewInvalid(req.GetObjectKind().GroupVersionKind().GroupKind(), req.Name, errs)
	}
	return nil, nil
}

func (r *invoiceRequestWebhook) ValidateUpdate(_ context.Context, oldReq, newReq *billingv1alpha1.InvoiceRequest) (admission.Warnings, error) {
	invoiceRequestLog.Info("validating update", "name", newReq.Name, "namespace", newReq.Namespace)

	if errs := validation.ValidateInvoiceRequestUpdate(oldReq, newReq); len(errs) > 0 {
		return nil, errors.NewInvalid(newReq.GetObjectKind().GroupVersionKind().GroupKind(), newReq.Name, errs)
	}
	return nil, nil
}

func (r *invoiceRequestWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.InvoiceRequest) (admission.Warnings, error) {
	return nil, nil
}
