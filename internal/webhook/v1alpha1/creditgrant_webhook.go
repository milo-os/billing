// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
	"go.miloapis.com/billing/pkg/features"
)

var creditGrantLog = logf.Log.WithName("creditgrant-webhook")

// SetupCreditGrantWebhookWithManager registers the CreditGrant defaulting
// and validating webhooks with the manager.
func SetupCreditGrantWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &creditGrantWebhook{
		reader: mgr.GetAPIReader(),
	}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.CreditGrant{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-creditgrant,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=creditgrants,verbs=create,versions=v1alpha1,name=mcreditgrant.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-creditgrant,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=creditgrants,verbs=create;update;delete,versions=v1alpha1,name=vcreditgrant.kb.io,admissionReviewVersions=v1

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=creditgrants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=creditgrants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get

type creditGrantWebhook struct {
	reader client.Reader
}

var (
	_ admission.Defaulter[*billingv1alpha1.CreditGrant] = &creditGrantWebhook{}
	_ admission.Validator[*billingv1alpha1.CreditGrant] = &creditGrantWebhook{}
)

// Default sets an owner reference to the BillingAccount so credit grants
// are garbage collected with their account.
func (r *creditGrantWebhook) Default(ctx context.Context, grant *billingv1alpha1.CreditGrant) error {
	if !utilfeature.DefaultFeatureGate.Enabled(features.CreditGrants) {
		return nil
	}
	if req, err := admission.RequestFromContext(ctx); err == nil && req.Operation != admissionv1.Create {
		return nil
	}
	addBillingAccountOwnerReference(ctx, r.reader, grant, grant.Spec.BillingAccountRef.Name)
	return nil
}

// ValidateCreate validates a CreditGrant on creation.
func (r *creditGrantWebhook) ValidateCreate(ctx context.Context, grant *billingv1alpha1.CreditGrant) (admission.Warnings, error) {
	creditGrantLog.Info("validating create", "name", grant.GetName(), "namespace", grant.GetNamespace())

	if !utilfeature.DefaultFeatureGate.Enabled(features.CreditGrants) {
		return nil, errors.NewForbidden(
			billingv1alpha1.GroupVersion.WithResource("creditgrants").GroupResource(),
			grant.Name,
			fmt.Errorf("credit grants feature gate is not enabled"),
		)
	}

	if errs := validation.ValidateCreditGrantCreate(ctx, r.reader, grant); len(errs) > 0 {
		return nil, errors.NewInvalid(
			grant.GetObjectKind().GroupVersionKind().GroupKind(),
			grant.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate validates a CreditGrant on update.
func (r *creditGrantWebhook) ValidateUpdate(ctx context.Context, oldGrant, newGrant *billingv1alpha1.CreditGrant) (admission.Warnings, error) {
	creditGrantLog.Info("validating update", "name", newGrant.GetName(), "namespace", newGrant.GetNamespace())

	if errs := validation.ValidateCreditGrantUpdate(ctx, r.reader, oldGrant, newGrant); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newGrant.GetObjectKind().GroupVersionKind().GroupKind(),
			newGrant.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete validates a CreditGrant on deletion.
func (r *creditGrantWebhook) ValidateDelete(ctx context.Context, grant *billingv1alpha1.CreditGrant) (admission.Warnings, error) {
	creditGrantLog.Info("validating delete", "name", grant.GetName(), "namespace", grant.GetNamespace())

	if errs := validation.ValidateCreditGrantDelete(ctx, r.reader, grant); len(errs) > 0 {
		return nil, errors.NewInvalid(
			grant.GetObjectKind().GroupVersionKind().GroupKind(),
			grant.Name,
			errs,
		)
	}

	return nil, nil
}
