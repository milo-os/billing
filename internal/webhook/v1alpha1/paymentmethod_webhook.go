// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var paymentMethodLog = logf.Log.WithName("paymentmethod-webhook")

// SetupPaymentMethodWebhookWithManager registers the PaymentMethod
// defaulting and validating webhooks.
//
// Defaulter responsibilities:
//   - Inject spec.paymentMethodClassRef from the cluster-default
//     PaymentMethodClass when the consumer omits it.
//
// Validator responsibilities:
//   - Ensure the referenced BillingAccount exists in the same namespace.
//   - Ensure a default PaymentMethodClass exists at creation time when
//     none was supplied.
func SetupPaymentMethodWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &paymentMethodWebhook{client: mgr.GetClient()}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&billingv1alpha1.PaymentMethod{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-paymentmethod,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=paymentmethods,verbs=create;update,versions=v1alpha1,name=mpaymentmethod.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-paymentmethod,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=paymentmethods,verbs=create;update,versions=v1alpha1,name=vpaymentmethod.kb.io,admissionReviewVersions=v1

type paymentMethodWebhook struct {
	client client.Client
}

var (
	_ admission.CustomDefaulter = &paymentMethodWebhook{}
	_ admission.CustomValidator = &paymentMethodWebhook{}
)

func (r *paymentMethodWebhook) Default(ctx context.Context, obj runtime.Object) error {
	pm, ok := obj.(*billingv1alpha1.PaymentMethod)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}
	paymentMethodLog.Info("defaulting", "name", pm.Name, "namespace", pm.Namespace)

	if pm.Spec.PaymentMethodClassRef != nil && pm.Spec.PaymentMethodClassRef.Name != "" {
		return nil
	}

	defaultClass, err := r.findDefaultClass(ctx)
	if err != nil {
		// Defer to ValidateCreate to produce a user-facing error.
		return nil
	}
	if defaultClass == nil {
		return nil
	}
	pm.Spec.PaymentMethodClassRef = &billingv1alpha1.PaymentMethodClassRef{Name: defaultClass.Name}
	return nil
}

func (r *paymentMethodWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	pm, ok := obj.(*billingv1alpha1.PaymentMethod)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}
	paymentMethodLog.Info("validating create", "name", pm.Name)

	var errs field.ErrorList

	if pm.Spec.PaymentMethodClassRef == nil || pm.Spec.PaymentMethodClassRef.Name == "" {
		errs = append(errs, field.Required(
			field.NewPath("spec", "paymentMethodClassRef"),
			"no default PaymentMethodClass exists; ask a platform operator to create one and label it with billing.miloapis.com/is-default-class=true, or set spec.paymentMethodClassRef explicitly",
		))
	}

	errs = append(errs, r.validateBillingAccountRef(ctx, pm)...)

	if len(errs) > 0 {
		return nil, errors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), pm.Name, errs)
	}
	return nil, nil
}

func (r *paymentMethodWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldPM, ok := oldObj.(*billingv1alpha1.PaymentMethod)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", oldObj)
	}
	newPM, ok := newObj.(*billingv1alpha1.PaymentMethod)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", newObj)
	}
	paymentMethodLog.Info("validating update", "name", newPM.Name)

	var errs field.ErrorList

	// Defence-in-depth alongside the XValidation rule on the spec — covers
	// older API servers that don't enforce CEL transition rules.
	if oldPM.Spec.PaymentMethodClassRef != nil &&
		(newPM.Spec.PaymentMethodClassRef == nil ||
			oldPM.Spec.PaymentMethodClassRef.Name != newPM.Spec.PaymentMethodClassRef.Name) {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "paymentMethodClassRef"),
			"paymentMethodClassRef is immutable once set",
		))
	}

	errs = append(errs, r.validateBillingAccountRef(ctx, newPM)...)

	if len(errs) > 0 {
		return nil, errors.NewInvalid(newObj.GetObjectKind().GroupVersionKind().GroupKind(), newPM.Name, errs)
	}
	return nil, nil
}

func (r *paymentMethodWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	pm, ok := obj.(*billingv1alpha1.PaymentMethod)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", obj)
	}
	paymentMethodLog.Info("validating delete", "name", pm.Name)

	// Reject deletion if the PaymentMethod is currently designated as
	// the default for its BillingAccount — the owner must explicitly
	// reassign or clear defaultPaymentMethodRef first.
	var ba billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: pm.Namespace, Name: pm.Spec.BillingAccountRef.Name}
	if err := r.client.Get(ctx, key, &ba); err != nil {
		if errors.IsNotFound(err) {
			// Owning account is gone; nothing references this PM.
			return nil, nil
		}
		// On read failure allow deletion — failing closed here would let
		// transient API hiccups block legitimate cleanup.
		paymentMethodLog.Error(err, "failed to read BillingAccount during delete validation, allowing deletion", "key", key)
		return nil, nil
	}
	if ba.Spec.DefaultPaymentMethodRef != nil && ba.Spec.DefaultPaymentMethodRef.Name == pm.Name {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			pm.Name,
			field.ErrorList{field.Forbidden(
				field.NewPath("metadata", "name"),
				fmt.Sprintf("payment method %q is the default for billing account %q; update spec.defaultPaymentMethodRef on the billing account before deleting", pm.Name, ba.Name),
			)},
		)
	}
	return nil, nil
}

func (r *paymentMethodWebhook) validateBillingAccountRef(ctx context.Context, pm *billingv1alpha1.PaymentMethod) field.ErrorList {
	var errs field.ErrorList
	fld := field.NewPath("spec", "billingAccountRef", "name")
	if pm.Spec.BillingAccountRef.Name == "" {
		errs = append(errs, field.Required(fld, "billingAccountRef.name is required"))
		return errs
	}

	var ba billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: pm.Namespace, Name: pm.Spec.BillingAccountRef.Name}
	if err := r.client.Get(ctx, key, &ba); err != nil {
		if errors.IsNotFound(err) {
			errs = append(errs, field.NotFound(fld, pm.Spec.BillingAccountRef.Name))
		} else {
			paymentMethodLog.Error(err, "failed to read BillingAccount during validation", "key", key)
			errs = append(errs, field.InternalError(fld, fmt.Errorf("reading billing account: %w", err)))
		}
	}
	return errs
}

func (r *paymentMethodWebhook) findDefaultClass(ctx context.Context) (*billingv1alpha1.PaymentMethodClass, error) {
	var list billingv1alpha1.PaymentMethodClassList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		billingv1alpha1.IsDefaultPaymentMethodClassLabel: "true",
	}); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	// The validating webhook prevents more than one default class
	// from being admitted, but a label selector with no uniqueness
	// guarantee at the API level can still return >1 if two writes
	// race past the webhook. Pick deterministically by name.
	winner := &list.Items[0]
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i].Name < winner.Name {
			winner = &list.Items[i]
		}
	}
	return winner, nil
}
