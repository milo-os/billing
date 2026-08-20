// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var offerLog = logf.Log.WithName("offer-webhook")

// SetupOfferWebhookWithManager registers the Offer webhook with the manager.
func SetupOfferWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &offerWebhook{Client: mgr.GetClient()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.Offer{}).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-offer,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=offers,verbs=create;update,versions=v1alpha1,name=voffer.kb.io,admissionReviewVersions=v1

type offerWebhook struct {
	client.Client
}

var _ admission.Validator[*billingv1alpha1.Offer] = &offerWebhook{}

// callerCanWriteSnapshot reports whether the admission caller holds
// billing.miloapis.com/offers.writeSnapshot. The controller's identity is
// determined by IAM authorization, not by comparing usernames in the webhook.
func (r *offerWebhook) callerCanWriteSnapshot(ctx context.Context, user authenticationv1.UserInfo, offerName string) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Verb:     validation.OfferSnapshotWriteVerb,
				Group:    billingv1alpha1.GroupVersion.Group,
				Resource: "offers",
				Name:     offerName,
			},
			User:   user.Username,
			Groups: user.Groups,
			UID:    user.UID,
			Extra:  convertAuthExtra(user.Extra),
		},
	}
	if err := r.Create(ctx, sar); err != nil {
		offerLog.Error(err, "failed to evaluate SubjectAccessReview for Offer snapshot write",
			"offer", offerName, "user", user.Username)
		return false, fmt.Errorf("couldn't verify permissions to write Offer pricing snapshot: %w", err)
	}
	return sar.Status.Allowed, nil
}

func convertAuthExtra(in map[string]authenticationv1.ExtraValue) map[string]authorizationv1.ExtraValue {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]authorizationv1.ExtraValue, len(in))
	for k, v := range in {
		out[k] = authorizationv1.ExtraValue(v)
	}
	return out
}

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
	if validation.IsControllerSnapshotFill(oldObj, newObj) {
		req, err := admission.RequestFromContext(ctx)
		if err != nil {
			offerLog.Error(err, "failed to retrieve admission request; denying snapshot writes")
		} else {
			allowed, sarErr := r.callerCanWriteSnapshot(ctx, req.UserInfo, newObj.GetName())
			switch {
			case sarErr != nil:
				offerLog.Error(sarErr, "denying servicePricings snapshot write; authorization check failed",
					"offer", newObj.GetName(), "user", req.UserInfo.Username)
			case allowed:
				opts.AllowSnapshotWrite = true
			default:
				offerLog.Info("denying servicePricings snapshot write; caller lacks offers.writeSnapshot",
					"offer", newObj.GetName(), "user", req.UserInfo.Username)
			}
		}
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
