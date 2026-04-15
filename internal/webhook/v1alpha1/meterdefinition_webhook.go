// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var meterDefinitionLog = logf.Log.WithName("meterdefinition-webhook")

// SetupMeterDefinitionWebhookWithManager registers the MeterDefinition
// webhook with the manager.
func SetupMeterDefinitionWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &meterDefinitionWebhook{
		client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&billingv1alpha1.MeterDefinition{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-meterdefinition,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=meterdefinitions,verbs=create;update,versions=v1alpha1,name=mmeterdefinition.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-meterdefinition,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=meterdefinitions,verbs=create;update;delete,versions=v1alpha1,name=vmeterdefinition.kb.io,admissionReviewVersions=v1

type meterDefinitionWebhook struct {
	client client.Client
}

var _ admission.CustomDefaulter = &meterDefinitionWebhook{}
var _ admission.CustomValidator = &meterDefinitionWebhook{}

// Default implements webhook.CustomDefaulter.
func (r *meterDefinitionWebhook) Default(ctx context.Context, obj runtime.Object) error {
	md, ok := obj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}

	meterDefinitionLog.Info("defaulting", "name", md.GetName())

	// ChargeCategory default is handled by the CRD-level +kubebuilder:default
	// marker. The defaulter is reserved for defaults that require cross-field
	// or external context.

	_ = md
	return nil
}

// ValidateCreate implements webhook.CustomValidator.
func (r *meterDefinitionWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	md, ok := obj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	meterDefinitionLog.Info("validating create",
		"name", md.GetName(),
		"meterName", md.Spec.MeterName,
		"owner", md.Spec.Owner.Service,
	)

	opts := validation.MeterDefinitionValidationOptions{
		Context: ctx,
		Client:  r.client,
	}

	if errs := validation.ValidateMeterDefinitionCreate(md, opts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(
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

	opts := validation.MeterDefinitionValidationOptions{
		Context: ctx,
		Client:  r.client,
	}

	if errs := validation.ValidateMeterDefinitionUpdate(oldMD, newMD, opts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newMD.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator. No-op for now; when
// pricing and entitlement resources land, this should reject deletion
// while references exist (matching the BillingAccount finalizer posture).
func (r *meterDefinitionWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	md, ok := obj.(*billingv1alpha1.MeterDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	meterDefinitionLog.Info("validating delete", "name", md.GetName())
	return nil, nil
}
