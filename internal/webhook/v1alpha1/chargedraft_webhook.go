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

var chargeDraftLog = logf.Log.WithName("chargedraft-webhook")

// SetupChargeDraftWebhookWithManager registers the ChargeDraft defaulting
// and validating webhooks.
func SetupChargeDraftWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &chargeDraftWebhook{reader: mgr.GetAPIReader()}
	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.ChargeDraft{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-chargedraft,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=chargedrafts,verbs=create,versions=v1alpha1,name=mchargedraft.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-chargedraft,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=chargedrafts,verbs=create;update,versions=v1alpha1,name=vchargedraft.kb.io,admissionReviewVersions=v1

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=chargedrafts,verbs=get;list;watch

type chargeDraftWebhook struct {
	reader client.Reader
	// now returns the current time; time.Now when unset.
	now func() time.Time
}

var (
	_ admission.Defaulter[*billingv1alpha1.ChargeDraft] = &chargeDraftWebhook{}
	_ admission.Validator[*billingv1alpha1.ChargeDraft] = &chargeDraftWebhook{}
)

func (r *chargeDraftWebhook) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// Default adds, on create, an owner reference to the billing account, so a
// draft goes with its account. A draft's lines aren't filled in: billing
// does that for each Charge when the draft is sent, as of then.
func (r *chargeDraftWebhook) Default(ctx context.Context, draft *billingv1alpha1.ChargeDraft) error {
	if req, err := admission.RequestFromContext(ctx); err == nil && req.Operation != admissionv1.Create {
		return nil
	}
	var account billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: draft.Namespace, Name: draft.Spec.BillingAccountRef.Name}
	if draft.Spec.BillingAccountRef.Name == "" || r.reader.Get(ctx, key, &account) != nil {
		return nil
	}
	addOwnerReference(draft, "BillingAccount", account.Name, account.UID)
	return nil
}

// ValidateCreate validates a ChargeDraft on creation.
func (r *chargeDraftWebhook) ValidateCreate(ctx context.Context, draft *billingv1alpha1.ChargeDraft) (admission.Warnings, error) {
	chargeDraftLog.Info("validating create", "name", draft.Name, "namespace", draft.Namespace)
	return nil, r.validate(ctx, draft)
}

// ValidateUpdate validates a ChargeDraft on every change.
func (r *chargeDraftWebhook) ValidateUpdate(ctx context.Context, _, draft *billingv1alpha1.ChargeDraft) (admission.Warnings, error) {
	chargeDraftLog.Info("validating update", "name", draft.Name, "namespace", draft.Namespace)
	return nil, r.validate(ctx, draft)
}

// ValidateDelete lets a draft be discarded at any time.
func (r *chargeDraftWebhook) ValidateDelete(context.Context, *billingv1alpha1.ChargeDraft) (admission.Warnings, error) {
	return nil, nil
}

func (r *chargeDraftWebhook) validate(ctx context.Context, draft *billingv1alpha1.ChargeDraft) error {
	if !draft.DeletionTimestamp.IsZero() {
		return nil
	}
	if errs := validation.ValidateChargeDraft(ctx, r.reader, draft, r.clock()); len(errs) > 0 {
		return errors.NewInvalid(billingv1alpha1.GroupVersion.WithKind("ChargeDraft").GroupKind(), draft.Name, errs)
	}
	return nil
}
