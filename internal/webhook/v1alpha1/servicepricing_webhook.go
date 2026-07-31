// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var servicePricingLog = logf.Log.WithName("servicepricing-webhook")

// SetupServicePricingWebhookWithManager registers the ServicePricing webhook
// with the manager.
func SetupServicePricingWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &servicePricingWebhook{}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.ServicePricing{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-servicepricing,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=servicepricings,verbs=create;update,versions=v1alpha1,name=vservicepricing.kb.io,admissionReviewVersions=v1

type servicePricingWebhook struct{}

var _ admission.Validator[*billingv1alpha1.ServicePricing] = &servicePricingWebhook{}

// ValidateCreate implements admission.Validator.
func (r *servicePricingWebhook) ValidateCreate(_ context.Context, obj *billingv1alpha1.ServicePricing) (admission.Warnings, error) {
	servicePricingLog.Info("validating create", "name", obj.GetName(), "namespace", obj.GetNamespace())

	if errs := validation.ValidateServicePricingCreate(obj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *servicePricingWebhook) ValidateUpdate(_ context.Context, oldObj, newObj *billingv1alpha1.ServicePricing) (admission.Warnings, error) {
	servicePricingLog.Info("validating update", "name", newObj.GetName(), "namespace", newObj.GetNamespace())

	if errs := validation.ValidateServicePricingUpdate(oldObj, newObj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *servicePricingWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.ServicePricing) (admission.Warnings, error) {
	return nil, nil
}
