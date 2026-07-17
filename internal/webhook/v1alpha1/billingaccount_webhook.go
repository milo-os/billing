// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
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

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingAccount{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingaccount,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update,versions=v1alpha1,name=mbillingaccount.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingaccount,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update;delete,versions=v1alpha1,name=vbillingaccount.kb.io,admissionReviewVersions=v1

type billingAccountWebhook struct {
	client client.Client
}

var _ admission.Defaulter[*billingv1alpha1.BillingAccount] = &billingAccountWebhook{}
var _ admission.Validator[*billingv1alpha1.BillingAccount] = &billingAccountWebhook{}

// Default implements admission.Defaulter.
func (r *billingAccountWebhook) Default(_ context.Context, _ *billingv1alpha1.BillingAccount) error {
	return nil
}

// ValidateCreate implements admission.Validator.
func (r *billingAccountWebhook) ValidateCreate(ctx context.Context, obj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating create", "name", obj.GetName())

	errs := validation.ValidateBillingAccountCreate(obj)
	errs = append(errs, validation.ValidateBillingAccountDefaultPaymentMethodRef(ctx, r.client, obj)...)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *billingAccountWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating update", "name", newObj.GetName())

	errs := validation.ValidateBillingAccountUpdate(oldObj, newObj)
	if !defaultPaymentMethodRefUnchanged(oldObj, newObj) {
		errs = append(errs, validation.ValidateBillingAccountDefaultPaymentMethodRef(ctx, r.client, newObj)...)
	}
	if len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

func defaultPaymentMethodRefUnchanged(oldAccount, newAccount *billingv1alpha1.BillingAccount) bool {
	oldName := ""
	if oldAccount.Spec.DefaultPaymentMethodRef != nil {
		oldName = oldAccount.Spec.DefaultPaymentMethodRef.Name
	}
	newName := ""
	if newAccount.Spec.DefaultPaymentMethodRef != nil {
		newName = newAccount.Spec.DefaultPaymentMethodRef.Name
	}
	return oldName == newName
}

// ValidateDelete implements admission.Validator.
// Belt-and-suspenders with the controller finalizer: reject deletion if active
// bindings reference this account.
func (r *billingAccountWebhook) ValidateDelete(ctx context.Context, obj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating delete", "name", obj.GetName())

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList, client.InNamespace(obj.Namespace)); err != nil {
		// If we can't list bindings, allow deletion -- the finalizer will catch it.
		billingAccountLog.Error(err, "failed to list bindings for delete validation, allowing deletion")
		return nil, nil
	}

	for i := range bindingList.Items {
		binding := &bindingList.Items[i]
		if binding.Spec.BillingAccountRef.Name == obj.Name &&
			binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			var allErrs field.ErrorList
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("metadata", "name"),
				fmt.Sprintf("billing account has active binding %q for project %q; remove all bindings before deleting",
					binding.Name, binding.Spec.ProjectRef.Name),
			))
			return nil, errors.NewInvalid(
				obj.GetObjectKind().GroupVersionKind().GroupKind(),
				obj.Name,
				allErrs,
			)
		}
	}

	return nil, nil
}
