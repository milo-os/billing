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

var monitoredResourceTypeLog = logf.Log.WithName("monitoredresourcetype-webhook")

// SetupMonitoredResourceTypeWebhookWithManager registers the MonitoredResourceType
// webhook with the manager.
func SetupMonitoredResourceTypeWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &monitoredResourceTypeWebhook{}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.MonitoredResourceType{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-monitoredresourcetype,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=monitoredresourcetypes,verbs=create;update;delete,versions=v1alpha1,name=vmonitoredresourcetype.kb.io,admissionReviewVersions=v1

type monitoredResourceTypeWebhook struct{}

var _ admission.Validator[*billingv1alpha1.MonitoredResourceType] = &monitoredResourceTypeWebhook{}

// ValidateCreate implements admission.Validator.
func (r *monitoredResourceTypeWebhook) ValidateCreate(_ context.Context, obj *billingv1alpha1.MonitoredResourceType) (admission.Warnings, error) {
	monitoredResourceTypeLog.Info("validating create", "name", obj.GetName())

	if errs := validation.ValidateMonitoredResourceTypeCreate(obj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *monitoredResourceTypeWebhook) ValidateUpdate(_ context.Context, oldObj, newObj *billingv1alpha1.MonitoredResourceType) (admission.Warnings, error) {
	monitoredResourceTypeLog.Info("validating update", "name", newObj.GetName())

	if errs := validation.ValidateMonitoredResourceTypeUpdate(oldObj, newObj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *monitoredResourceTypeWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.MonitoredResourceType) (admission.Warnings, error) {
	// Allow deletion; GC handles cleanup.
	return nil, nil
}
