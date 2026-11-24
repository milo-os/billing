// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

var billingAccountLog = logf.Log.WithName("billingaccount-webhook")

// SetupBillingAccountWebhookWithManager registers the BillingAccount webhook
// with the manager.
func SetupBillingAccountWebhookWithManager(mgr ctrl.Manager) error {
	webhook := &billingAccountWebhook{
		client: mgr.GetClient(),
		// The default payment method check reads the card straight from
		// the API server: a card that turned Active moments ago may still
		// look AwaitingConfirmation in the cache.
		reader: mgr.GetAPIReader(),
	}

	return ctrl.NewWebhookManagedBy(mgr, &billingv1alpha1.BillingAccount{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-billing-miloapis-com-v1alpha1-billingaccount,mutating=true,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update,versions=v1alpha1,name=mbillingaccount.kb.io,admissionReviewVersions=v1

// +kubebuilder:webhook:path=/validate-billing-miloapis-com-v1alpha1-billingaccount,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.miloapis.com,resources=billingaccounts,verbs=create;update;delete,versions=v1alpha1,name=vbillingaccount.kb.io,admissionReviewVersions=v1

type billingAccountWebhook struct {
	client client.Client
	reader client.Reader
}

var _ admission.Defaulter[*billingv1alpha1.BillingAccount] = &billingAccountWebhook{}
var _ admission.Validator[*billingv1alpha1.BillingAccount] = &billingAccountWebhook{}

// Default implements admission.Defaulter.
func (r *billingAccountWebhook) Default(_ context.Context, _ *billingv1alpha1.BillingAccount) error {
	return nil
}

// ValidateCreate implements admission.Validator.
func (r *billingAccountWebhook) ValidateCreate(ctx context.Context, obj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating create", "name", obj.GetName())

	errs := validation.ValidateBillingAccountCreate(obj)
	errs = append(errs, validation.ValidateBillingAccountDefaultPaymentMethodRef(ctx, r.reader, obj)...)
	errs = append(errs, validation.ValidateBillingAccountDefaultForNewProjects(ctx, r.reader, obj)...)
	if len(errs) > 0 {
		return nil, errors.NewInvalid(
			obj.GetObjectKind().GroupVersionKind().GroupKind(),
			obj.Name,
			errs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (r *billingAccountWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating update", "name", newObj.GetName())

	errs := validation.ValidateBillingAccountUpdate(oldObj, newObj)
	errs = append(errs, r.contactDetailsEditorLimits(ctx, oldObj, newObj)...)
	if !defaultPaymentMethodRefUnchanged(oldObj, newObj) {
		errs = append(errs, validation.ValidateBillingAccountDefaultPaymentMethodRef(ctx, r.reader, newObj)...)
	}
	if newObj.Spec.DefaultForNewProjects && !oldObj.Spec.DefaultForNewProjects {
		errs = append(errs, validation.ValidateBillingAccountDefaultForNewProjects(ctx, r.reader, newObj)...)
	}
	if len(errs) > 0 {
		return nil, errors.NewInvalid(
			newObj.GetObjectKind().GroupVersionKind().GroupKind(),
			newObj.Name,
			errs,
		)
	}

	return nil, nil
}

// contactDetailsEditorLimits refuses an update from a contact-details editor
// (staff, through Billing Account Editor) that changes more than the contact
// details and tax IDs: the default card, default for new projects, the
// currency, labels or annotations. A caller who can also manage the
// account's settings, such as a member of staff in their own organization,
// isn't limited. See validation.BillingAccountEditContactDetailsVerb.
func (r *billingAccountWebhook) contactDetailsEditorLimits(ctx context.Context, oldObj, newObj *billingv1alpha1.BillingAccount) field.ErrorList {
	changed, err := validation.BillingAccountSettingsChanges(oldObj, newObj)
	if err != nil {
		return field.ErrorList{field.InternalError(field.NewPath("spec"), err)}
	}
	if len(changed) == 0 {
		return nil
	}

	// Fail closed: without the caller, the limit can't be checked.
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return field.ErrorList{field.InternalError(changed[0], fmt.Errorf("couldn't read the admission request: %w", err))}
	}
	editor, err := r.callerCan(ctx, req.UserInfo, validation.BillingAccountEditContactDetailsVerb, newObj)
	if err != nil {
		return field.ErrorList{field.InternalError(changed[0], err)}
	}
	if !editor {
		return nil
	}
	manager, err := r.callerCan(ctx, req.UserInfo, validation.BillingAccountManageSettingsVerb, newObj)
	if err != nil {
		return field.ErrorList{field.InternalError(changed[0], err)}
	}
	if manager {
		return nil
	}

	billingAccountLog.Info("refusing a contact-details editor's change to the account's settings",
		"name", newObj.GetName(), "user", req.UserInfo.Username)
	errs := make(field.ErrorList, 0, len(changed))
	for _, path := range changed {
		errs = append(errs, field.Forbidden(path,
			"your role can change only a billing account's contact details, address and tax IDs"))
	}
	return errs
}

// callerCan reports whether the admission caller holds verb on the account.
// The caller's extras go along, since they carry the organization Milo
// authorizes the request in.
func (r *billingAccountWebhook) callerCan(ctx context.Context, user authenticationv1.UserInfo, verb string, account *billingv1alpha1.BillingAccount) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Verb:      verb,
				Group:     billingv1alpha1.GroupVersion.Group,
				Resource:  "billingaccounts",
				Namespace: account.Namespace,
				Name:      account.Name,
			},
			User:   user.Username,
			Groups: user.Groups,
			UID:    user.UID,
			Extra:  convertAuthExtra(user.Extra),
		},
	}
	if err := r.client.Create(ctx, sar); err != nil {
		return false, fmt.Errorf("couldn't check the caller's %s permission: %w", verb, err)
	}
	return sar.Status.Allowed, nil
}

func defaultPaymentMethodRefUnchanged(oldAccount, newAccount *billingv1alpha1.BillingAccount) bool {
	oldName := ""
	if oldAccount.Spec.DefaultPaymentMethodRef != nil {
		oldName = oldAccount.Spec.DefaultPaymentMethodRef.Name
	}
	newName := ""
	if newAccount.Spec.DefaultPaymentMethodRef != nil {
		newName = newAccount.Spec.DefaultPaymentMethodRef.Name
	}
	return oldName == newName
}

// ValidateDelete implements admission.Validator.
// Belt-and-suspenders with the controller finalizer: reject deletion if active
// bindings reference this account.
func (r *billingAccountWebhook) ValidateDelete(ctx context.Context, obj *billingv1alpha1.BillingAccount) (admission.Warnings, error) {
	billingAccountLog.Info("validating delete", "name", obj.GetName())

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList, client.InNamespace(obj.Namespace)); err != nil {
		// If we can't list bindings, allow deletion -- the finalizer will catch it.
		billingAccountLog.Error(err, "failed to list bindings for delete validation, allowing deletion")
		return nil, nil
	}

	for i := range bindingList.Items {
		binding := &bindingList.Items[i]
		if binding.Spec.BillingAccountRef.Name == obj.Name &&
			binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			var allErrs field.ErrorList
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("metadata", "name"),
				fmt.Sprintf("billing account has active binding %q for project %q; remove all bindings before deleting",
					binding.Name, binding.Spec.ProjectRef.Name),
			))
			return nil, errors.NewInvalid(
				obj.GetObjectKind().GroupVersionKind().GroupKind(),
				obj.Name,
				allErrs,
			)
		}
	}

	return nil, nil
}
