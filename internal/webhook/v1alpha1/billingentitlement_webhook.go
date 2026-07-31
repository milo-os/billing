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

var billingEntitlementLog = logf.Log.WithName("billingentitlement-webhook")

// SetupBillingEntitlementWebhookWithManager registers the BillingEntitlement
// webhook with the manager.
func SetupBillingEntitlementWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingEntitlementWebhook{
		client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingEntitlement{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingentitlement,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingentitlements,verbs=create;update,versions=v1alpha1,name=vbillingentitlement.kb.io,admissionReviewVersions=v1

type billingEntitlementWebhook struct {
	client client.Client
}

var _ admission.Validator[*billingv1alpha1.BillingEntitlement] = &billingEntitlementWebhook{}

// ValidateCreate implements admission.Validator.
func (r *billingEntitlementWebhook) ValidateCreate(ctx context.Context, obj *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	billingEntitlementLog.Info("validating create", "name", obj.GetName(), "namespace", obj.GetNamespace())

	if errs := validation.ValidateBillingEntitlementCreate(ctx, r.client, obj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *billingEntitlementWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	billingEntitlementLog.Info("validating update", "name", newObj.GetName(), "namespace", newObj.GetNamespace())

	if errs := validation.ValidateBillingEntitlementUpdate(ctx, r.client, oldObj, newObj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *billingEntitlementWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	return nil, nil
}
