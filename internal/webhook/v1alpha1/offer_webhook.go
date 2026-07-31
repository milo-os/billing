// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var offerLog = logf.Log.WithName("offer-webhook")

// SetupOfferWebhookWithManager registers the Offer webhook with the manager.
// billingOperatorSA is the username allowed to write the one-time
// servicePricings snapshot (typically
// system:serviceaccount:billing-system:billing-controller-manager).
func SetupOfferWebhookWithManager(mgr ctrl.Manager, billingOperatorSA string) error {
	webhook := &offerWebhook{billingOperatorSA: billingOperatorSA}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.Offer{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-offer,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=offers,verbs=create;update,versions=v1alpha1,name=voffer.kb.io,admissionReviewVersions=v1

type offerWebhook struct {
	billingOperatorSA string
}

var _ admission.Validator[*billingv1alpha1.Offer] = &offerWebhook{}

// ValidateCreate implements admission.Validator.
func (r *offerWebhook) ValidateCreate(_ context.Context, obj *billingv1alpha1.Offer) (admission.Warnings, error) {
	offerLog.Info("validating create", "name", obj.GetName())

	if errs := validation.ValidateOfferCreate(obj); len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *offerWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj *billingv1alpha1.Offer) (admission.Warnings, error) {
	offerLog.Info("validating update", "name", newObj.GetName())

	opts := validation.OfferUpdateOptions{}
	if req, err := admission.RequestFromContext(ctx); err != nil {
		offerLog.Error(err, "failed to retrieve admission request; denying snapshot writes")
	} else if req.UserInfo.Username == r.billingOperatorSA {
		opts.AllowSnapshotWrite = true
	}

	if errs := validation.ValidateOfferUpdate(oldObj, newObj, opts); len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (r *offerWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.Offer) (admission.Warnings, error) {
	return nil, nil
}