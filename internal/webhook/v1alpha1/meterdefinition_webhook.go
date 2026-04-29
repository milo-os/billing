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

var meterDefinitionLog = logf.Log.WithName("meterdefinition-webhook")

// SetupMeterDefinitionWebhookWithManager registers the MeterDefinition webhook
// with the manager.
func SetupMeterDefinitionWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &meterDefinitionWebhook{}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&billingv1alpha1.MeterDefinition{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-meterdefinition,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=meterdefinitions,verbs=create;update;delete,versions=v1alpha1,name=vmeterdefinition.kb.io,admissionReviewVersions=v1

type meterDefinitionWebhook struct{}

var _ admission.CustomValidator = &meterDefinitionWebhook{}

// ValidateCreate implements webhook.CustomValidator.
func (r *meterDefinitionWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	md, ok := obj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	meterDefinitionLog.Info("validating create", "name", md.GetName())

	if errs := validation.ValidateMeterDefinitionCreate(md); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			md.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *meterDefinitionWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldMD, ok := oldObj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", oldObj)
	}

	newMD, ok := newObj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", newObj)
	}

	meterDefinitionLog.Info("validating update", "name", newMD.GetName())

	if errs := validation.ValidateMeterDefinitionUpdate(oldMD, newMD); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newMD.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (r *meterDefinitionWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// Allow deletion; GC handles cleanup.
	return nil, nil
}
