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
)

var paymentMethodClassLog = logf.Log.WithName("paymentmethodclass-webhook")

// SetupPaymentMethodClassWebhookWithManager registers the
// PaymentMethodClass validating webhook with the manager. The webhook's
// sole responsibility is to keep the cluster-default-class invariant:
// exactly one PaymentMethodClass may carry the
// `billing.miloapis.com/is-default-class=true` label at any time.
func SetupPaymentMethodClassWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &paymentMethodClassWebhook{client: mgr.GetClient()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.PaymentMethodClass{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-paymentmethodclass,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=paymentmethodclasses,verbs=create;update,versions=v1alpha1,name=vpaymentmethodclass.kb.io,admissionReviewVersions=v1

type paymentMethodClassWebhook struct {
	client client.Client
}

var _ admission.Validator[*billingv1alpha1.PaymentMethodClass] = &paymentMethodClassWebhook{}

func (r *paymentMethodClassWebhook) ValidateCreate(ctx context.Context, class *billingv1alpha1.PaymentMethodClass) (admission.Warnings, error) {
	paymentMethodClassLog.Info("validating create", "name", class.Name)
	return nil, r.validateDefaultUniqueness(ctx, class, "")
}

func (r *paymentMethodClassWebhook) ValidateUpdate(ctx context.Context, _, class *billingv1alpha1.PaymentMethodClass) (admission.Warnings, error) {
	paymentMethodClassLog.Info("validating update", "name", class.Name)
	return nil, r.validateDefaultUniqueness(ctx, class, class.Name)
}

func (r *paymentMethodClassWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.PaymentMethodClass) (admission.Warnings, error) {
	return nil, nil
}

// validateDefaultUniqueness rejects the operation if the candidate class
// is marked default AND a different class already holds the same label.
// `selfName` allows the on-update path to skip the candidate itself
// (so re-applying the same resource is idempotent).
func (r *paymentMethodClassWebhook) validateDefaultUniqueness(ctx context.Context, candidate *billingv1alpha1.PaymentMethodClass, selfName string) error {
	if candidate.Labels[billingv1alpha1.IsDefaultPaymentMethodClassLabel] != "true" {
		return nil
	}

	var classList billingv1alpha1.PaymentMethodClassList
	if err := r.client.List(ctx, &classList, client.MatchingLabels{
		billingv1alpha1.IsDefaultPaymentMethodClassLabel: "true",
	}); err != nil {
		// Fail-closed on a list error — we cannot safely admit a
		// potentially duplicate default class.
		return errors.NewInternalError(fmt.Errorf("listing PaymentMethodClasses: %w", err))
	}
	for i := range classList.Items {
		existing := &classList.Items[i]
		if existing.Name == selfName {
			continue
		}
		return errors.NewInvalid(
			candidate.GetObjectKind().GroupVersionKind().GroupKind(),
			candidate.Name,
			field.ErrorList{field.Forbidden(
				field.NewPath("metadata", "labels").Key(billingv1alpha1.IsDefaultPaymentMethodClassLabel),
				fmt.Sprintf("PaymentMethodClass %q is already the default; only one default class is permitted", existing.Name),
			)},
		)
	}
	return nil
}
