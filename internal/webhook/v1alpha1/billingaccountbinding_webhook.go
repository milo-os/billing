// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingAccountBindingLog = logf.Log.WithName("billingaccountbinding-webhook")

// SetupBillingAccountBindingWebhookWithManager registers the BillingAccountBinding
// webhook with the manager.
func SetupBillingAccountBindingWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingAccountBindingWebhook{
		client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingAccountBinding{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingaccountbinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccountbindings,verbs=create;update;delete,versions=v1alpha1,name=vbillingaccountbinding.kb.io,admissionReviewVersions=v1

type billingAccountBindingWebhook struct {
	client client.Client
}

var _ admission.Validator[*billingv1alpha1.BillingAccountBinding] = &billingAccountBindingWebhook{}

// ValidateCreate implements admission.Validator.
func (r *billingAccountBindingWebhook) ValidateCreate(ctx context.Context, obj *billingv1alpha1.BillingAccountBinding) (admission.Warnings, error) {
	billingAccountBindingLog.Info("validating create",
		"name", obj.GetName(),
		"project", obj.Spec.ProjectRef.Name,
		"account", obj.Spec.BillingAccountRef.Name,
	)

	opts := validation.BillingAccountBindingValidationOptions{
		Context: ctx,
		Client:  r.client,
	}

	if errs := validation.ValidateBillingAccountBindingCreate(obj, opts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *billingAccountBindingWebhook) ValidateUpdate(_ context.Context, oldObj, newObj *billingv1alpha1.BillingAccountBinding) (admission.Warnings, error) {
	billingAccountBindingLog.Info("validating update", "name", newObj.GetName())

	// Spec immutability is enforced by XValidation on the CRD.
	// Belt-and-suspenders: also reject spec changes in the webhook.
	var allErrs field.ErrorList
	if oldObj.Spec.BillingAccountRef.Name != newObj.Spec.BillingAccountRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "billingAccountRef", "name"),
			"field is immutable",
		))
	}
	if oldObj.Spec.ProjectRef.Name != newObj.Spec.ProjectRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "projectRef", "name"),
			"field is immutable",
		))
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *billingAccountBindingWebhook) ValidateDelete(_ context.Context, obj *billingv1alpha1.BillingAccountBinding) (admission.Warnings, error) {
	billingAccountBindingLog.Info("validating delete", "name", obj.GetName())
	return nil, nil
}
