// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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

	return ctrl.NewWebhookManagedBy(mgr).
		For(&billingv1alpha1.MonitoredResourceType{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-monitoredresourcetype,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=monitoredresourcetypes,verbs=create;update;delete,versions=v1alpha1,name=vmonitoredresourcetype.kb.io,admissionReviewVersions=v1

type monitoredResourceTypeWebhook struct{}

var _ admission.CustomValidator = &monitoredResourceTypeWebhook{}

// ValidateCreate implements webhook.CustomValidator.
func (r *monitoredResourceTypeWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mrt, ok := obj.(*billingv1alpha1.MonitoredResourceType)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	monitoredResourceTypeLog.Info("validating create", "name", mrt.GetName())

	if errs := validation.ValidateMonitoredResourceTypeCreate(mrt); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			mrt.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *monitoredResourceTypeWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldMRT, ok := oldObj.(*billingv1alpha1.MonitoredResourceType)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", oldObj)
	}

	newMRT, ok := newObj.(*billingv1alpha1.MonitoredResourceType)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", newObj)
	}

	monitoredResourceTypeLog.Info("validating update", "name", newMRT.GetName())

	if errs := validation.ValidateMonitoredResourceTypeUpdate(oldMRT, newMRT); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newMRT.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (r *monitoredResourceTypeWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// Allow deletion; GC handles cleanup.
	return nil, nil
}
