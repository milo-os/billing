// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
	"go.miloapis.com/billing/pkg/features"
)

var billingEntitlementLog = logf.Log.WithName("billingentitlement-webhook")

// SetupBillingEntitlementWebhookWithManager registers the BillingEntitlement
// webhook with the manager.
func SetupBillingEntitlementWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingEntitlementWebhook{
		client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingEntitlement{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingentitlement,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingentitlements,verbs=create;update,versions=v1alpha1,name=vbillingentitlement.kb.io,admissionReviewVersions=v1

type billingEntitlementWebhook struct {
	client client.Client
	now    func() time.Time
}

var _ admission.Validator[*billingv1alpha1.BillingEntitlement] = &billingEntitlementWebhook{}

// ValidateCreate implements admission.Validator.
func (r *billingEntitlementWebhook) ValidateCreate(ctx context.Context, obj *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	billingEntitlementLog.Info("validating create", "name", obj.GetName(), "namespace", obj.GetNamespace())

	errs := validation.ValidateBillingEntitlementCreate(ctx, r.client, obj, r.clock())
	errs = append(errs, validateContractStopGate(nil, obj)...)
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
func (r *billingEntitlementWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	billingEntitlementLog.Info("validating update", "name", newObj.GetName(), "namespace", newObj.GetNamespace())

	errs := validation.ValidateBillingEntitlementUpdate(ctx, r.client, oldObj, newObj, r.clock())
	errs = append(errs, validateContractStopGate(oldObj, newObj)...)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *billingEntitlementWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.BillingEntitlement) (admission.Warnings, error) {
	return nil, nil
}

// validateContractStopGate refuses setting a contract's term to stop while
// the ContractStop feature gate is off. A term already set to stop can
// still be changed. oldObj is nil on create.
func validateContractStopGate(oldObj, newObj *billingv1alpha1.BillingEntitlement) field.ErrorList {
	if utilfeature.DefaultFeatureGate.Enabled(features.ContractStop) || !setToStop(newObj) || (oldObj != nil && setToStop(oldObj)) {
		return nil
	}
	return field.ErrorList{field.Forbidden(field.NewPath("spec", "contract", "term", "atEnd"),
		"a contract can't be set to stop yet: the ContractStop feature gate is disabled")}
}

func setToStop(be *billingv1alpha1.BillingEntitlement) bool {
	return be.Spec.Contract != nil && be.Spec.Contract.Term != nil &&
		be.Spec.Contract.Term.AtEnd == billingv1alpha1.ContractTermAtEndStop
}

func (r *billingEntitlementWebhook) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}
