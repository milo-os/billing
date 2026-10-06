// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingArrangementLog = logf.Log.WithName("billingarrangement-webhook")

// SetupBillingArrangementWebhookWithManager registers the
// BillingArrangement defaulting and validating webhooks.
//
// Defaulter responsibilities:
//   - Set spec.startsAt to the admission time on create when omitted, so
//     every arrangement records an explicit start.
//   - Add an owner reference to the BillingAccount on create, so
//     arrangements are garbage collected with their account and a new
//     account reusing the name never inherits them.
//
// Validator responsibilities:
//   - Ensure the referenced BillingAccount exists in the same namespace.
//   - Ensure endsAt is after startsAt and invoice terms are well formed.
//   - Ensure arrangements for one billing account never overlap.
func SetupBillingArrangementWebhookWithManager(mgr ctrl.Manager) error {
	// Reads go straight to the API server: the overlap check has to see
	// arrangements admitted moments ago, which the cache may not have.
	webhook := &billingArrangementWebhook{reader: mgr.GetAPIReader()}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingArrangement{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingarrangement,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangements,verbs=create,versions=v1alpha1,name=mbillingarrangement.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingarrangement,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingarrangements,verbs=create;update,versions=v1alpha1,name=vbillingarrangement.kb.io,admissionReviewVersions=v1

type billingArrangementWebhook struct {
	reader client.Reader
	now    func() time.Time
}

var (
	_ admission.Defaulter[*billingv1alpha1.BillingArrangement] = &billingArrangementWebhook{}
	_ admission.Validator[*billingv1alpha1.BillingArrangement] = &billingArrangementWebhook{}
)

func (r *billingArrangementWebhook) Default(ctx context.Context, arr *billingv1alpha1.BillingArrangement) error {
	// Only default on create: clearing startsAt on update must not move
	// an existing arrangement's start to now.
	if req, err := admission.RequestFromContext(ctx); err == nil && req.Operation != admissionv1.Create {
		return nil
	}
	if arr.Spec.StartsAt == nil {
		start := metav1.NewTime(r.clock().Truncate(time.Second))
		arr.Spec.StartsAt = &start
	}
	r.defaultOwnerReference(ctx, arr)
	return nil
}

// defaultOwnerReference points the arrangement at its BillingAccount.
// A missing or unreadable account is left for ValidateCreate to report.
// The reference isn't a controller reference and doesn't block owner
// deletion: arrangements never hold up deleting an account.
func (r *billingArrangementWebhook) defaultOwnerReference(ctx context.Context, arr *billingv1alpha1.BillingArrangement) {
	var account billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: arr.Namespace, Name: arr.Spec.BillingAccountRef.Name}
	if arr.Spec.BillingAccountRef.Name == "" || r.reader.Get(ctx, key, &account) != nil {
		return
	}
	for _, ref := range arr.OwnerReferences {
		if ref.UID == account.UID {
			return
		}
	}
	arr.OwnerReferences = append(arr.OwnerReferences, metav1.OwnerReference{
		APIVersion: billingv1alpha1.GroupVersion.String(),
		Kind:       "BillingAccount",
		Name:       account.Name,
		UID:        account.UID,
	})
}

func (r *billingArrangementWebhook) ValidateCreate(ctx context.Context, arr *billingv1alpha1.BillingArrangement) (admission.Warnings, error) {
	billingArrangementLog.Info("validating create", "name", arr.Name, "namespace", arr.Namespace)

	if errs := validation.ValidateBillingArrangementCreate(ctx, r.reader, arr, r.clock()); len(errs) > 0 {
		return nil, errors.NewInvalid(arr.GetObjectKind().GroupVersionKind().GroupKind(), arr.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementWebhook) ValidateUpdate(ctx context.Context, oldArr, newArr *billingv1alpha1.BillingArrangement) (admission.Warnings, error) {
	billingArrangementLog.Info("validating update", "name", newArr.Name, "namespace", newArr.Namespace)

	if errs := validation.ValidateBillingArrangementUpdate(ctx, r.reader, oldArr, newArr, r.clock()); len(errs) > 0 {
		return nil, errors.NewInvalid(newArr.GetObjectKind().GroupVersionKind().GroupKind(), newArr.Name, errs)
	}
	return nil, nil
}

func (r *billingArrangementWebhook) ValidateDelete(_ context.Context, _ *billingv1alpha1.BillingArrangement) (admission.Warnings, error) {
	return nil, nil
}

func (r *billingArrangementWebhook) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}
