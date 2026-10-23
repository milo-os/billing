// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingArrangementApplicationLog = logf.Log.WithName("billingarrangementapplication-webhook")

// SetupBillingArrangementApplicationWebhookWithManager registers the
// BillingArrangementApplication defaulting and validating webhooks.
//
// Defaulter responsibilities:
//   - Set spec.applicant to the authenticated user on create, replacing
//     anything the client sent, so the applicant can't be forged.
//   - Add an owner reference to the BillingAccount on create.
//
// Validator responsibilities:
//   - Ensure the referenced BillingAccount exists and has billing contact
//     details and an address.
//   - Ensure the details match the payment option and are well formed.
//   - Ensure only one application per billing account is pending, and
//     that the account doesn't already have open-ended or scheduled terms.
//   - Ensure nothing but spec.withdrawn changes after submission, and
//     that only a pending application can be withdrawn.
//   - Ensure the application is owned only by its billing account, and
//     that the owner reference can't be changed or removed while the
//     account exists.
func SetupBillingArrangementApplicationWebhookWithManager(mgr ctrl.Manager) error {
	// Reads go straight to the API server: the pending checks have to see
	// applications and decisions admitted moments ago.
	webhook := &billingArrangementApplicationWebhook{reader: mgr.GetAPIReader()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingArrangementApplication{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingarrangementapplication,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangementapplications,verbs=create,versions=v1alpha1,name=mbillingarrangementapplication.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingarrangementapplication,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangementapplications,verbs=create;update,versions=v1alpha1,name=vbillingarrangementapplication.kb.io,admissionReviewVersions=v1

type billingArrangementApplicationWebhook struct {
	reader client.Reader
	now    func() time.Time
}

var (
	_ admission.Defaulter[*billingv1alpha1.BillingArrangementApplication] = &billingArrangementApplicationWebhook{}
	_ admission.Validator[*billingv1alpha1.BillingArrangementApplication] = &billingArrangementApplicationWebhook{}
)

func (r *billingArrangementApplicationWebhook) Default(ctx context.Context, app *billingv1alpha1.BillingArrangementApplication) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil || req.Operation != admissionv1.Create {
		return nil
	}
	app.Spec.Applicant = &billingv1alpha1.Applicant{
		Username: req.UserInfo.Username,
		UID:      req.UserInfo.UID,
	}
	addBillingAccountOwnerReference(ctx, r.reader, app, app.Spec.BillingAccountRef.Name)
	return nil
}

func (r *billingArrangementApplicationWebhook) ValidateCreate(ctx context.Context, app *billingv1alpha1.BillingArrangementApplication) (admission.Warnings, error) {
	billingArrangementApplicationLog.Info("validating create", "name", app.Name, "namespace", app.Namespace)

	if errs := validation.ValidateBillingArrangementApplicationCreate(ctx, r.reader, app, r.clock()); len(errs) > 0 {
		return nil, errors.NewInvalid(app.GetObjectKind().GroupVersionKind().GroupKind(), app.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementApplicationWebhook) ValidateUpdate(ctx context.Context, oldApp, newApp *billingv1alpha1.BillingArrangementApplication) (admission.Warnings, error) {
	billingArrangementApplicationLog.Info("validating update", "name", newApp.Name, "namespace", newApp.Namespace)

	if errs := validation.ValidateBillingArrangementApplicationUpdate(ctx, r.reader, oldApp, newApp); len(errs) > 0 {
		return nil, errors.NewInvalid(newApp.GetObjectKind().GroupVersionKind().GroupKind(), newApp.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementApplicationWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.BillingArrangementApplication) (admission.Warnings, error) {
	return nil, nil
}

func (r *billingArrangementApplicationWebhook) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}
