// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// ApplicationDecision is the staff decision recorded for a
// BillingArrangementApplication: the arrangement that approved it, or
// the decline. At most one is set.
type ApplicationDecision struct {
	Arrangement *billingv1alpha1.BillingArrangement
	Decline     *billingv1alpha1.BillingArrangementDecline
}

// Decided reports whether staff approved or declined the application.
func (d ApplicationDecision) Decided() bool {
	return d.Arrangement != nil || d.Decline != nil
}

// FindApplicationDecision picks the decision for app from the
// arrangements and declines in its namespace. The webhooks allow one
// decision per application, but if a race lets two through, an approval
// wins over a decline (terms really were granted), then the oldest.
//
// Decisions that are being deleted, or that predate the application,
// are skipped. One older than the application decided an earlier
// application with the same name.
func FindApplicationDecision(
	app *billingv1alpha1.BillingArrangementApplication,
	arrs []billingv1alpha1.BillingArrangement,
	declines []billingv1alpha1.BillingArrangementDecline,
) ApplicationDecision {
	var d ApplicationDecision
	for i := range arrs {
		arr := &arrs[i]
		if arr.Spec.ApplicationRef == nil || arr.Spec.ApplicationRef.Name != app.Name || !decides(arr, app) {
			continue
		}
		if d.Arrangement == nil || olderObject(arr, d.Arrangement) {
			d.Arrangement = arr
		}
	}
	if d.Arrangement != nil {
		return d
	}
	for i := range declines {
		decline := &declines[i]
		if decline.Spec.ApplicationRef.Name != app.Name || !decides(decline, app) {
			continue
		}
		if d.Decline == nil || olderObject(decline, d.Decline) {
			d.Decline = decline
		}
	}
	return d
}

// ApplicationPhase derives an application's phase from its decision and
// spec.withdrawn. A decision outranks a withdrawal that raced it.
func ApplicationPhase(app *billingv1alpha1.BillingArrangementApplication, d ApplicationDecision) billingv1alpha1.BillingArrangementApplicationPhase {
	switch {
	case d.Arrangement != nil:
		return billingv1alpha1.BillingArrangementApplicationPhaseApproved
	case d.Decline != nil:
		return billingv1alpha1.BillingArrangementApplicationPhaseDeclined
	case app.Spec.Withdrawn:
		return billingv1alpha1.BillingArrangementApplicationPhaseWithdrawn
	default:
		return billingv1alpha1.BillingArrangementApplicationPhasePending
	}
}

// OwnedByOtherBillingAccount reports whether obj is owned by a
// BillingAccount other than the one with accountUID: it belonged to a
// deleted account with the same name and is waiting for garbage
// collection. An empty accountUID skips nothing.
func OwnedByOtherBillingAccount(obj metav1.Object, accountUID types.UID) bool {
	if accountUID == "" {
		return false
	}
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "BillingAccount" && ref.UID != accountUID {
			return true
		}
	}
	return false
}

func decides(decision, app metav1.Object) bool {
	if decision.GetDeletionTimestamp() != nil {
		return false
	}
	created, appCreated := decision.GetCreationTimestamp(), app.GetCreationTimestamp()
	return !created.Before(&appCreated)
}

func olderObject(a, b metav1.Object) bool {
	ta, tb := a.GetCreationTimestamp(), b.GetCreationTimestamp()
	if ta.Equal(&tb) {
		return a.GetName() < b.GetName()
	}
	return ta.Before(&tb)
}

// ValidateBillingArrangementApplicationCreate validates a
// BillingArrangementApplication on creation. now decides which of the
// account's arrangements are scheduled or active.
//
// c should be an uncached reader: the one-pending-application check
// must see applications and decisions admitted moments ago.
func ValidateBillingArrangementApplicationCreate(
	ctx context.Context,
	c client.Reader,
	app *billingv1alpha1.BillingArrangementApplication,
	now time.Time,
) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	account, errs := getReferencedBillingAccount(ctx, c, app.Namespace, app.Spec.BillingAccountRef.Name)
	allErrs = append(allErrs, errs...)
	if account != nil {
		if contact := account.Spec.ContactInfo; contact == nil || contact.Email == "" || contact.Address == nil {
			allErrs = append(allErrs, field.Forbidden(specPath.Child("billingAccountRef", "name"),
				fmt.Sprintf("add billing contact details and a billing address to billing account %q before applying for payment terms", account.Name)))
		}
		allErrs = append(allErrs, validateApplicationOwnerReferencesOnCreate(app, account)...)
	}

	allErrs = append(allErrs, validateApplicationSpec(app)...)

	if app.Spec.Applicant == nil || app.Spec.Applicant.Username == "" {
		allErrs = append(allErrs, field.Required(specPath.Child("applicant"),
			"applicant is set from the authenticated user by the admission webhook"))
	}
	if app.Spec.Withdrawn {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("withdrawn"),
			"an application can't be withdrawn when it's submitted"))
	}

	if account != nil {
		allErrs = append(allErrs, validateAccountCanApply(ctx, c, app, account, now)...)
	}
	return allErrs
}

// ValidateBillingArrangementApplicationUpdate validates a
// BillingArrangementApplication on update. Only spec.withdrawn may
// change, from false to true, and only while the application is
// pending. The CEL rules on the CRD enforce the same, apart from the
// pending check. Owner references are held fixed too (see
// validateApplicationOwnerReferencesOnUpdate), which CEL can't see.
func ValidateBillingArrangementApplicationUpdate(
	ctx context.Context,
	c client.Reader,
	oldApp, newApp *billingv1alpha1.BillingArrangementApplication,
) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, validateApplicationOwnerReferencesOnUpdate(ctx, c, oldApp, newApp)...)

	oldSpec, newSpec := oldApp.Spec.DeepCopy(), newApp.Spec.DeepCopy()
	oldSpec.Withdrawn, newSpec.Withdrawn = false, false
	if !equality.Semantic.DeepEqual(oldSpec, newSpec) {
		allErrs = append(allErrs, field.Forbidden(specPath,
			"an application can't be changed after it's submitted; withdraw it and apply again"))
	}

	switch {
	case oldApp.Spec.Withdrawn && !newApp.Spec.Withdrawn:
		allErrs = append(allErrs, field.Forbidden(specPath.Child("withdrawn"),
			"a withdrawn application can't be reopened; apply again instead"))
	case !oldApp.Spec.Withdrawn && newApp.Spec.Withdrawn:
		arrs, declines, err := listApplicationDecisions(ctx, c, newApp.Namespace)
		if err != nil {
			return append(allErrs, field.InternalError(specPath.Child("withdrawn"), err))
		}
		if d := FindApplicationDecision(oldApp, arrs, declines); d.Decided() {
			allErrs = append(allErrs, field.Forbidden(specPath.Child("withdrawn"),
				fmt.Sprintf("the application has already been %s and can't be withdrawn", decisionVerb(d))))
		}
	}
	return allErrs
}

// validateApplicationSpec checks what the CRD schema can't, and repeats
// its cross-field rules for defence in depth.
func validateApplicationSpec(app *billingv1alpha1.BillingArrangementApplication) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	switch app.Spec.PaymentOption {
	case billingv1alpha1.ApplicationPaymentOptionInvoice,
		billingv1alpha1.ApplicationPaymentOptionNotSure:
	default:
		allErrs = append(allErrs, field.NotSupported(specPath.Child("paymentOption"), app.Spec.PaymentOption,
			[]billingv1alpha1.ApplicationPaymentOption{
				billingv1alpha1.ApplicationPaymentOptionInvoice,
				billingv1alpha1.ApplicationPaymentOptionNotSure,
			}))
	}

	isInvoice := app.Spec.PaymentOption == billingv1alpha1.ApplicationPaymentOptionInvoice
	switch {
	case isInvoice && app.Spec.Invoice == nil:
		allErrs = append(allErrs, field.Required(specPath.Child("invoice"), "invoice is required when paymentOption is Invoice"))
	case !isInvoice && app.Spec.Invoice != nil:
		allErrs = append(allErrs, field.Forbidden(specPath.Child("invoice"), "invoice may only be set when paymentOption is Invoice"))
	}

	if inv := app.Spec.Invoice; inv != nil {
		allErrs = append(allErrs, validatePlainEmail(inv.AccountsPayableEmail, specPath.Child("invoice", "accountsPayableEmail"))...)
	}

	websitePath := specPath.Child("organization", "website")
	if u, err := url.Parse(app.Spec.Organization.Website); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		allErrs = append(allErrs, field.Invalid(websitePath, app.Spec.Organization.Website, "must be an http or https URL"))
	}

	if !app.Spec.Authorized {
		allErrs = append(allErrs, field.Invalid(specPath.Child("authorized"), false,
			"the applicant must confirm they're authorized to request payment terms for the organization"))
	}
	return allErrs
}

// validateAccountCanApply rejects an application when the billing
// account can't take one now:
//   - another application for it is awaiting review. Pending is derived
//     from decisions rather than read from status, which may lag. Two
//     creates racing each other can both pass this check; both then show
//     up in the review queue and staff decline one, so that's accepted.
//   - it has terms with no end date, or terms scheduled to start, so
//     there's nothing to apply for. Active terms with an end date can be
//     renewed by applying again.
func validateAccountCanApply(
	ctx context.Context,
	c client.Reader,
	app *billingv1alpha1.BillingArrangementApplication,
	account *billingv1alpha1.BillingAccount,
	now time.Time,
) field.ErrorList {
	fldPath := field.NewPath("spec", "billingAccountRef", "name")

	var apps billingv1alpha1.BillingArrangementApplicationList
	if err := c.List(ctx, &apps, client.InNamespace(app.Namespace)); err != nil {
		return field.ErrorList{field.InternalError(fldPath, fmt.Errorf("listing billing arrangement applications: %w", err))}
	}
	arrs, declines, err := listApplicationDecisions(ctx, c, app.Namespace)
	if err != nil {
		return field.ErrorList{field.InternalError(fldPath, err)}
	}

	for i := range apps.Items {
		other := &apps.Items[i]
		if other.Name == app.Name || !other.DeletionTimestamp.IsZero() ||
			other.Spec.BillingAccountRef.Name != account.Name || OwnedByOtherBillingAccount(other, account.UID) {
			continue
		}
		if ApplicationPhase(other, FindApplicationDecision(other, arrs, declines)) == billingv1alpha1.BillingArrangementApplicationPhasePending {
			return field.ErrorList{field.Forbidden(fldPath,
				fmt.Sprintf("application %q for billing account %q is already awaiting review", other.Name, account.Name))}
		}
	}

	for i := range arrs {
		arr := &arrs[i]
		if !arr.DeletionTimestamp.IsZero() || arr.Spec.BillingAccountRef.Name != account.Name ||
			OwnedByOtherBillingAccount(arr, account.UID) {
			continue
		}
		switch ArrangementPhaseAt(arr, now) {
		case billingv1alpha1.BillingArrangementPhaseScheduled:
			return field.ErrorList{field.Forbidden(fldPath,
				fmt.Sprintf("billing account %q already has payment terms starting at %s",
					account.Name, ArrangementStart(arr, arr.CreationTimestamp.Time).UTC().Format(time.RFC3339)))}
		case billingv1alpha1.BillingArrangementPhaseActive:
			if arr.Spec.EndsAt == nil {
				return field.ErrorList{field.Forbidden(fldPath,
					fmt.Sprintf("billing account %q already has payment terms with no end date", account.Name))}
			}
		}
	}
	return nil
}

// validateApplicationOwnerReferencesOnCreate allows an application to be
// owned only by the billing account it's for. Any other owner reference
// would let it outlive or be collected separately from that account.
func validateApplicationOwnerReferencesOnCreate(
	app *billingv1alpha1.BillingArrangementApplication,
	account *billingv1alpha1.BillingAccount,
) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("metadata", "ownerReferences")
	for i, ref := range app.OwnerReferences {
		if ref.Kind != "BillingAccount" || ref.UID != account.UID || ref.Name != account.Name {
			allErrs = append(allErrs, field.Forbidden(fldPath.Index(i),
				fmt.Sprintf("an application can only be owned by billing account %q", account.Name)))
		}
	}
	return allErrs
}

// validateApplicationOwnerReferencesOnUpdate holds an application's
// owner references fixed. Customers can update applications (to withdraw
// them), and pointing the billing account reference elsewhere would hide
// the application from the one-pending check and PaymentReady, then let
// the garbage collector delete it, which customers aren't allowed to do.
//
// The only change allowed is removing a reference whose owner is gone or
// being deleted, which is how the garbage collector orphans dependents.
func validateApplicationOwnerReferencesOnUpdate(
	ctx context.Context,
	c client.Reader,
	oldApp, newApp *billingv1alpha1.BillingArrangementApplication,
) field.ErrorList {
	fldPath := field.NewPath("metadata", "ownerReferences")

	for i, ref := range newApp.OwnerReferences {
		if !containsOwnerReference(oldApp.OwnerReferences, ref) {
			return field.ErrorList{field.Forbidden(fldPath.Index(i), "owner references can't be added or changed")}
		}
	}
	for _, ref := range oldApp.OwnerReferences {
		if containsOwnerReference(newApp.OwnerReferences, ref) || ref.Kind != "BillingAccount" {
			continue
		}
		var account billingv1alpha1.BillingAccount
		err := c.Get(ctx, types.NamespacedName{Namespace: oldApp.Namespace, Name: ref.Name}, &account)
		switch {
		case apierrors.IsNotFound(err):
			continue
		case err != nil:
			return field.ErrorList{field.InternalError(fldPath, fmt.Errorf("reading billing account: %w", err))}
		case account.UID == ref.UID && account.DeletionTimestamp.IsZero():
			return field.ErrorList{field.Forbidden(fldPath,
				fmt.Sprintf("the owner reference to billing account %q can't be removed while the account exists", ref.Name))}
		}
	}
	return nil
}

func containsOwnerReference(refs []metav1.OwnerReference, ref metav1.OwnerReference) bool {
	for _, r := range refs {
		if equality.Semantic.DeepEqual(r, ref) {
			return true
		}
	}
	return false
}

// ValidateBillingArrangementDeclineCreate validates a
// BillingArrangementDecline on creation: the application it declines
// must exist and still be pending.
func ValidateBillingArrangementDeclineCreate(
	ctx context.Context,
	c client.Reader,
	decline *billingv1alpha1.BillingArrangementDecline,
) field.ErrorList {
	_, errs := getPendingApplication(ctx, c, decline.Namespace, decline.Spec.ApplicationRef.Name)
	return errs
}

// ValidateBillingArrangementDeclineUpdate validates a
// BillingArrangementDecline on update. The message and internal notes
// may be corrected; applicationRef immutability is enforced by a CEL
// rule on the CRD and repeated here for defence in depth.
func ValidateBillingArrangementDeclineUpdate(oldDecline, newDecline *billingv1alpha1.BillingArrangementDecline) field.ErrorList {
	if oldDecline.Spec.ApplicationRef.Name != newDecline.Spec.ApplicationRef.Name {
		return field.ErrorList{field.Forbidden(field.NewPath("spec", "applicationRef"), "applicationRef is immutable")}
	}
	return nil
}

// validateArrangementApplicationRef checks that an arrangement approving
// an application names a pending application for the same billing
// account.
func validateArrangementApplicationRef(
	ctx context.Context,
	c client.Reader,
	arr *billingv1alpha1.BillingArrangement,
) field.ErrorList {
	if arr.Spec.ApplicationRef == nil {
		return nil
	}
	app, errs := getPendingApplication(ctx, c, arr.Namespace, arr.Spec.ApplicationRef.Name)
	if app != nil && app.Spec.BillingAccountRef.Name != arr.Spec.BillingAccountRef.Name {
		errs = append(errs, field.Invalid(field.NewPath("spec", "applicationRef", "name"), arr.Spec.ApplicationRef.Name,
			fmt.Sprintf("application is for billing account %q, not %q", app.Spec.BillingAccountRef.Name, arr.Spec.BillingAccountRef.Name)))
	}
	return errs
}

// getPendingApplication reads the application a decision names and
// checks it can still be decided: it exists, isn't being deleted, wasn't
// withdrawn and has no decision yet.
func getPendingApplication(
	ctx context.Context,
	c client.Reader,
	namespace, name string,
) (*billingv1alpha1.BillingArrangementApplication, field.ErrorList) {
	fldPath := field.NewPath("spec", "applicationRef", "name")
	if name == "" {
		return nil, field.ErrorList{field.Required(fldPath, "applicationRef.name is required")}
	}

	var app billingv1alpha1.BillingArrangementApplication
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, field.ErrorList{field.NotFound(fldPath, name)}
		}
		return nil, field.ErrorList{field.InternalError(fldPath, fmt.Errorf("reading billing arrangement application: %w", err))}
	}
	if !app.DeletionTimestamp.IsZero() {
		return nil, field.ErrorList{field.Forbidden(fldPath, fmt.Sprintf("application %q is being deleted", name))}
	}

	arrs, declines, err := listApplicationDecisions(ctx, c, namespace)
	if err != nil {
		return nil, field.ErrorList{field.InternalError(fldPath, err)}
	}
	d := FindApplicationDecision(&app, arrs, declines)
	switch {
	case d.Decided():
		return nil, field.ErrorList{field.Forbidden(fldPath,
			fmt.Sprintf("application %q has already been %s", name, decisionVerb(d)))}
	case app.Spec.Withdrawn:
		return nil, field.ErrorList{field.Forbidden(fldPath,
			fmt.Sprintf("application %q was withdrawn", name))}
	}
	return &app, nil
}

// listApplicationDecisions lists every arrangement and decline in a
// namespace. It lists rather than using field indexes because the
// indexes live in the cache and webhooks must read through to the API
// server. Decisions per organization are few.
func listApplicationDecisions(
	ctx context.Context,
	c client.Reader,
	namespace string,
) ([]billingv1alpha1.BillingArrangement, []billingv1alpha1.BillingArrangementDecline, error) {
	var arrs billingv1alpha1.BillingArrangementList
	if err := c.List(ctx, &arrs, client.InNamespace(namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing billing arrangements: %w", err)
	}
	var declines billingv1alpha1.BillingArrangementDeclineList
	if err := c.List(ctx, &declines, client.InNamespace(namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing billing arrangement declines: %w", err)
	}
	return arrs.Items, declines.Items, nil
}

func decisionVerb(d ApplicationDecision) string {
	if d.Arrangement != nil {
		return fmt.Sprintf("approved by arrangement %q", d.Arrangement.Name)
	}
	return "declined"
}

// applicationRefName is the name of the application an arrangement
// approves, or "" for terms granted without one.
func applicationRefName(arr *billingv1alpha1.BillingArrangement) string {
	if arr.Spec.ApplicationRef == nil {
		return ""
	}
	return arr.Spec.ApplicationRef.Name
}
