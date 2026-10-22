// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingArrangementDeclineLog = logf.Log.WithName("billingarrangementdecline-webhook")

// SetupBillingArrangementDeclineWebhookWithManager registers the
// BillingArrangementDecline defaulting and validating webhooks.
//
// Defaulter responsibilities:
//   - Add an owner reference to the declined application on create, so
//     the decline is removed with it.
//
// Validator responsibilities:
//   - Ensure the application exists and is still pending.
//   - Ensure applicationRef never changes.
func SetupBillingArrangementDeclineWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingArrangementDeclineWebhook{reader: mgr.GetAPIReader()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingArrangementDecline{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingarrangementdecline,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangementdeclines,verbs=create,versions=v1alpha1,name=mbillingarrangementdecline.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingarrangementdecline,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangementdeclines,verbs=create;update,versions=v1alpha1,name=vbillingarrangementdecline.kb.io,admissionReviewVersions=v1

type billingArrangementDeclineWebhook struct {
	reader client.Reader
}

var (
	_ admission.Defaulter[*billingv1alpha1.BillingArrangementDecline] = &billingArrangementDeclineWebhook{}
	_ admission.Validator[*billingv1alpha1.BillingArrangementDecline] = &billingArrangementDeclineWebhook{}
)

func (r *billingArrangementDeclineWebhook) Default(ctx context.Context, decline *billingv1alpha1.BillingArrangementDecline) error {
	if req, err := admission.RequestFromContext(ctx); err == nil && req.Operation != admissionv1.Create {
		return nil
	}
	// A missing or unreadable application is left for ValidateCreate to
	// report.
	var app billingv1alpha1.BillingArrangementApplication
	key := types.NamespacedName{Namespace: decline.Namespace, Name: decline.Spec.ApplicationRef.Name}
	if decline.Spec.ApplicationRef.Name == "" || r.reader.Get(ctx, key, &app) != nil {
		return nil
	}
	addOwnerReference(decline, "BillingArrangementApplication", app.Name, app.UID)
	return nil
}

func (r *billingArrangementDeclineWebhook) ValidateCreate(ctx context.Context, decline *billingv1alpha1.BillingArrangementDecline) (admission.Warnings, error) {
	billingArrangementDeclineLog.Info("validating create", "name", decline.Name, "namespace", decline.Namespace)

	if errs := validation.ValidateBillingArrangementDeclineCreate(ctx, r.reader, decline); len(errs) > 0 {
		return nil, errors.NewInvalid(decline.GetObjectKind().GroupVersionKind().GroupKind(), decline.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementDeclineWebhook) ValidateUpdate(_ context.Context, oldDecline, newDecline *billingv1alpha1.BillingArrangementDecline) (admission.Warnings, error) {
	billingArrangementDeclineLog.Info("validating update", "name", newDecline.Name, "namespace", newDecline.Namespace)

	if errs := validation.ValidateBillingArrangementDeclineUpdate(oldDecline, newDecline); len(errs) > 0 {
		return nil, errors.NewInvalid(newDecline.GetObjectKind().GroupVersionKind().GroupKind(), newDecline.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementDeclineWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.BillingArrangementDecline) (admission.Warnings, error) {
	return nil, nil
}
