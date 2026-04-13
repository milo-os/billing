// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
	billingwebhook "go.miloapis.com/billing/internal/webhook"
)

var billingAccountBindingLog = logf.Log.WithName("billingaccountbinding-webhook")

// SetupBillingAccountBindingWebhookWithManager registers the BillingAccountBinding
// webhook with the manager.
func SetupBillingAccountBindingWebhookWithManager(mgr mcmanager.Manager) error {
	webhook := &billingAccountBindingWebhook{
		mgr: mgr,
	}

	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).
		For(&billingv1alpha1.BillingAccountBinding{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingaccountbinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccountbindings,verbs=create;update;delete,versions=v1alpha1,name=vbillingaccountbinding.kb.io,admissionReviewVersions=v1

type billingAccountBindingWebhook struct {
	mgr mcmanager.Manager
}

var _ admission.CustomValidator = &billingAccountBindingWebhook{}

// getClusterClient returns the appropriate client for the cluster context.
// In multicluster mode, this resolves the cluster from the webhook context.
// In single-cluster mode, it falls back to the local manager client.
func (r *billingAccountBindingWebhook) getClusterClient(ctx context.Context) client.Client {
	clusterName := billingwebhook.ClusterNameFromContext(ctx)
	if clusterName != "" {
		if cl, err := r.mgr.GetCluster(ctx, clusterName); err == nil {
			return cl.GetClient()
		}
	}
	return r.mgr.GetLocalManager().GetClient()
}

// ValidateCreate implements webhook.CustomValidator.
func (r *billingAccountBindingWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	billingAccountBindingLog.Info("validating create",
		"name", binding.GetName(),
		"project", binding.Spec.ProjectRef.Name,
		"account", binding.Spec.BillingAccountRef.Name,
	)

	clusterClient := r.getClusterClient(ctx)

	opts := validation.BillingAccountBindingValidationOptions{
		Context: ctx,
		Client:  clusterClient,
	}

	if errs := validation.ValidateBillingAccountBindingCreate(binding, opts); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			binding.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *billingAccountBindingWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldBinding, ok := oldObj.(*billingv1alpha1.BillingAccountBinding)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", oldObj)
	}

	newBinding, ok := newObj.(*billingv1alpha1.BillingAccountBinding)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", newObj)
	}

	billingAccountBindingLog.Info("validating update", "name", newBinding.GetName())

	// Spec immutability is enforced by XValidation on the CRD.
	// Belt-and-suspenders: also reject spec changes in the webhook.
	var allErrs field.ErrorList
	if oldBinding.Spec.BillingAccountRef.Name != newBinding.Spec.BillingAccountRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "billingAccountRef", "name"),
			"field is immutable",
		))
	}
	if oldBinding.Spec.ProjectRef.Name != newBinding.Spec.ProjectRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "projectRef", "name"),
			"field is immutable",
		))
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newBinding.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (r *billingAccountBindingWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}

	billingAccountBindingLog.Info("validating delete", "name", binding.GetName())

	return nil, nil
}
