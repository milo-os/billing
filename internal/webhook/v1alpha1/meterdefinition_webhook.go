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

var meterDefinitionLog = logf.Log.WithName("meterdefinition-webhook")

// SetupMeterDefinitionWebhookWithManager registers the MeterDefinition webhook
// with the manager.
func SetupMeterDefinitionWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &meterDefinitionWebhook{}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.MeterDefinition{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-meterdefinition,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=meterdefinitions,verbs=create;update;delete,versions=v1alpha1,name=vmeterdefinition.kb.io,admissionReviewVersions=v1

type meterDefinitionWebhook struct{}

var _ admission.Validator[*billingv1alpha1.MeterDefinition] = &meterDefinitionWebhook{}

// ValidateCreate implements admission.Validator.
func (r *meterDefinitionWebhook) ValidateCreate(_ context.Context, obj *billingv1alpha1.MeterDefinition) (admission.Warnings, error) {
	meterDefinitionLog.Info("validating create", "name", obj.GetName())

	if errs := validation.ValidateMeterDefinitionCreate(obj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *meterDefinitionWebhook) ValidateUpdate(_ context.Context, oldObj, newObj *billingv1alpha1.MeterDefinition) (admission.Warnings, error) {
	meterDefinitionLog.Info("validating update", "name", newObj.GetName())

	if errs := validation.ValidateMeterDefinitionUpdate(oldObj, newObj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *meterDefinitionWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.MeterDefinition) (admission.Warnings, error) {
	// Allow deletion; GC handles cleanup.
	return nil, nil
}
