// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"time"

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

var chargeLog = logf.Log.WithName("charge-webhook")

// SetupChargeWebhookWithManager registers the Charge defaulting and
// validating webhooks with the manager.
func SetupChargeWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &chargeWebhook{reader: mgr.GetAPIReader()}
	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.Charge{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-charge,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=charges,verbs=create,versions=v1alpha1,name=mcharge.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-charge,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=charges,verbs=create;update;delete,versions=v1alpha1,name=vcharge.kb.io,admissionReviewVersions=v1

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=charges,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=servicepricings,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get

type chargeWebhook struct {
	reader client.Reader
	// now returns the current time; time.Now when unset.
	now func() time.Time
}

var (
	_ admission.Defaulter[*billingv1alpha1.Charge] = &chargeWebhook{}
	_ admission.Validator[*billingv1alpha1.Charge] = &chargeWebhook{}
)

func (r *chargeWebhook) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// Default fills in, on create, what staff left out: the catalog item's
// description, price and tax, the account's current billing period, who
// added the charge, and an owner reference to the billing account.
func (r *chargeWebhook) Default(ctx context.Context, charge *billingv1alpha1.Charge) error {
	req, err := admission.RequestFromContext(ctx)
	if err == nil && req.Operation != admissionv1.Create {
		return nil
	}

	var account *billingv1alpha1.BillingAccount
	if name := charge.Spec.BillingAccountRef.Name; name != "" {
		var a billingv1alpha1.BillingAccount
		if r.reader.Get(ctx, types.NamespacedName{Namespace: charge.Namespace, Name: name}, &a) == nil {
			account = &a
		}
	}
	var pricing *billingv1alpha1.ServicePricing
	if ref := charge.Spec.ServicePricingRef; ref != nil && ref.Name != "" {
		ns := ref.Namespace
		if ns == "" {
			ns = "milo-system"
		}
		var p billingv1alpha1.ServicePricing
		if r.reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &p) == nil {
			pricing = &p
		}
	}
	validation.ApplyChargeDefaults(&charge.Spec, pricing, account, r.clock())

	if err == nil && req.UserInfo.Username != "" {
		annotations := charge.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[billingv1alpha1.ChargeAddedByAnnotation] = req.UserInfo.Username
		charge.SetAnnotations(annotations)
	}
	if account != nil {
		addOwnerReference(charge, "BillingAccount", account.Name, account.UID)
	}
	return nil
}

// ValidateCreate validates a Charge on creation.
func (r *chargeWebhook) ValidateCreate(ctx context.Context, charge *billingv1alpha1.Charge) (admission.Warnings, error) {
	chargeLog.Info("validating create", "name", charge.GetName(), "namespace", charge.GetNamespace())
	if errs := validation.ValidateChargeCreate(ctx, r.reader, charge, r.clock()); len(errs) > 0 {
		return nil, errors.NewInvalid(billingv1alpha1.GroupVersion.WithKind("Charge").GroupKind(), charge.Name, errs)
	}
	return nil, nil
}

// ValidateUpdate refuses any change to a charge's spec.
func (r *chargeWebhook) ValidateUpdate(_ context.Context, oldCharge, newCharge *billingv1alpha1.Charge) (admission.Warnings, error) {
	if errs := validation.ValidateChargeUpdate(oldCharge, newCharge); len(errs) > 0 {
		return nil, errors.NewInvalid(billingv1alpha1.GroupVersion.WithKind("Charge").GroupKind(), newCharge.Name, errs)
	}
	return nil, nil
}

// ValidateDelete refuses to remove an invoiced charge, except while its
// account or namespace is being deleted.
func (r *chargeWebhook) ValidateDelete(ctx context.Context, charge *billingv1alpha1.Charge) (admission.Warnings, error) {
	chargeLog.Info("validating delete", "name", charge.GetName(), "namespace", charge.GetNamespace())
	if errs := validation.ValidateChargeDelete(ctx, r.reader, charge); len(errs) > 0 {
		return nil, errors.NewInvalid(billingv1alpha1.GroupVersion.WithKind("Charge").GroupKind(), charge.Name, errs)
	}
	return nil, nil
}
