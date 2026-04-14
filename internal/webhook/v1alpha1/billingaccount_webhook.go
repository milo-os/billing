// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingAccountLog = logf.Log.WithName("billingaccount-webhook")

// SetupBillingAccountWebhookWithManager registers the BillingAccount webhook
// with the manager.
func SetupBillingAccountWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingAccountWebhook{
		client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&billingv1alpha1.BillingAccount{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingaccount,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update,versions=v1alpha1,name=mbillingaccount.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingaccount,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update;delete,versions=v1alpha1,name=vbillingaccount.kb.io,admissionReviewVersions=v1

type billingAccountWebhook struct {
	client client.Client
}

var _ admission.CustomDefaulter = &billingAccountWebhook{}
var _ admission.CustomValidator = &billingAccountWebhook{}

// Default implements webhook.CustomDefaulter.
func (r *billingAccountWebhook) Default(ctx context.Context, obj runtime.Object) error {
	account, ok := obj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}

	billingAccountLog.Info("defaulting", "name", account.GetName())

	// PaymentTerms field defaults (netDays, invoiceFrequency, invoiceDayOfMonth)
	// are handled by CRD-level +kubebuilder:default markers. The webhook defaulter
	// is reserved for defaults that require cross-field or external context.

	_ = account
	return nil
}

// ValidateCreate implements webhook.CustomValidator.
func (r *billingAccountWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	account, ok := obj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	billingAccountLog.Info("validating create", "name", account.GetName())

	if errs := validation.ValidateBillingAccountCreate(account); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			account.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *billingAccountWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldAccount, ok := oldObj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", oldObj)
	}

	newAccount, ok := newObj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", newObj)
	}

	billingAccountLog.Info("validating update", "name", newAccount.GetName())

	if errs := validation.ValidateBillingAccountUpdate(oldAccount, newAccount); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newAccount.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
// Belt-and-suspenders with the controller finalizer: reject deletion if active
// bindings reference this account.
func (r *billingAccountWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	account, ok := obj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	billingAccountLog.Info("validating delete", "name", account.GetName())

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList, client.InNamespace(account.Namespace)); err != nil {
		// If we can't list bindings, allow deletion -- the finalizer will catch it.
		billingAccountLog.Error(err, "failed to list bindings for delete validation, allowing deletion")
		return nil, nil
	}

	for i := range bindingList.Items {
		binding := &bindingList.Items[i]
		if binding.Spec.BillingAccountRef.Name == account.Name &&
			binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			var allErrs field.ErrorList
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("metadata", "name"),
				fmt.Sprintf("billing account has active binding %q for project %q; remove all bindings before deleting",
					binding.Name, binding.Spec.ProjectRef.Name),
			))
			return nil, errors.NewInvalid(
				obj.GetObjectKind().GroupVersionKind().GroupKind(),
				account.Name,
				allErrs,
			)
		}
	}

	return nil, nil
}
