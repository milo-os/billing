// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	billingv1alpha1ac "go.miloapis.com/billing/applyconfiguration/api/v1alpha1"
)

const (
	billingAccountFinalizer = "billing.miloapis.com/billing-account"

	// billingAccountFieldOwner is the field manager name used by this
	// reconciler when it server-side-applies finalizer changes. SSA
	// scopes our writes to the fields we declare in the apply payload,
	// so other managers' writes to spec / status / other finalizers
	// stay intact even when we apply a stale copy.
	billingAccountFieldOwner = "billing-controller"
)

// BillingAccountReconciler reconciles a BillingAccount object.
type BillingAccountReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethods,verbs=get;list;watch

func (r *BillingAccountReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var account billingv1alpha1.BillingAccount
	if err := r.client.Get(ctx, req.NamespacedName, &account); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if !account.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, r.client, &account)
	}

	// Ensure finalizer is present via Server-Side Apply. SSA declares
	// ownership of only the fields in the apply payload (here just
	// metadata.finalizers), so other managers' writes to spec — e.g.
	// contactInfo.invoiceEmails set by the portal at create time —
	// stay intact even when our local copy hasn't observed them yet.
	// A full Update would PUT our stale spec back and strip such
	// fields.
	if !controllerutil.ContainsFinalizer(&account, billingAccountFinalizer) {
		if err := r.client.Apply(ctx,
			billingv1alpha1ac.BillingAccount(account.Name, account.Namespace).
				WithFinalizers(billingAccountFinalizer),
			client.FieldOwner(billingAccountFieldOwner),
			client.ForceOwnership,
		); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Determine the target phase based on current state
	targetPhase := r.determinePhase(&account)

	// Count linked projects
	linkedCount, err := r.countActiveBindings(ctx, r.client, &account)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to count active bindings: %w", err)
	}

	// Update status
	account.Status.Phase = targetPhase
	account.Status.LinkedProjectsCount = linkedCount
	account.Status.ObservedGeneration = account.Generation

	// Set Ready condition
	if targetPhase == billingv1alpha1.BillingAccountPhaseReady {
		apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: account.Generation,
			Reason:             "BillingAccountReady",
			Message:            "Billing account is active and ready for project binding.",
		})
	} else {
		apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: account.Generation,
			Reason:             "BillingAccountNotReady",
			Message:            fmt.Sprintf("Billing account is in %s phase.", targetPhase),
		})
	}

	// Resolve and project the default-payment-method health onto status.
	r.reconcileDefaultPaymentMethodCondition(ctx, &account)

	if err := r.client.Status().Update(ctx, &account); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled billing account",
		"phase", targetPhase,
		"linkedProjects", linkedCount,
	)

	return ctrl.Result{}, nil
}

// determinePhase computes the target phase based on the account's current
// state. Suspended and Archived are managed externally (by admin or payment
// system); all other phases converge to Ready.
func (r *BillingAccountReconciler) determinePhase(account *billingv1alpha1.BillingAccount) billingv1alpha1.BillingAccountPhase {
	switch account.Status.Phase {
	case billingv1alpha1.BillingAccountPhaseSuspended,
		billingv1alpha1.BillingAccountPhaseArchived:
		return account.Status.Phase
	}
	return billingv1alpha1.BillingAccountPhaseReady
}

// countActiveBindings counts the number of active BillingAccountBindings
// that reference this account.
func (r *BillingAccountReconciler) countActiveBindings(
	ctx context.Context,
	cl client.Client,
	account *billingv1alpha1.BillingAccount,
) (int32, error) {
	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := cl.List(ctx, &bindingList,
		client.InNamespace(account.Namespace),
		client.MatchingFields{BindingBillingAccountRefField: account.Name},
	); err != nil {
		return 0, err
	}

	var count int32
	for i := range bindingList.Items {
		if bindingList.Items[i].Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
			count++
		}
	}

	return count, nil
}

// reconcileDelete handles the deletion of a BillingAccount.
func (r *BillingAccountReconciler) reconcileDelete(
	ctx context.Context,
	cl client.Client,
	account *billingv1alpha1.BillingAccount,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check for active bindings
	activeCount, err := r.countActiveBindings(ctx, cl, account)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check active bindings: %w", err)
	}

	if activeCount > 0 {
		logger.Info("billing account has active bindings, cannot finalize",
			"activeBindings", activeCount,
		)
		// Requeue to check again later
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Remove our finalizer via SSA: re-apply with no finalizers
	// list under our field owner. The apiserver drops our claim
	// on the entry while leaving other managers' finalizers (e.g.
	// amberflo-provider's customer-link finalizer) alone. See
	// the comment on the Add path above.
	if err := cl.Apply(ctx,
		billingv1alpha1ac.BillingAccount(account.Name, account.Namespace),
		client.FieldOwner(billingAccountFieldOwner),
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	logger.Info("finalized billing account")
	return ctrl.Result{}, nil
}

// reconcileDefaultPaymentMethodCondition keeps the
// DefaultPaymentMethodReady condition in lock-step with the configured
// defaultPaymentMethodRef and the phase of the referenced PaymentMethod.
// Failures to resolve the referenced resource are surfaced as a False
// condition; they never bubble up as errors because they're a
// configuration concern, not a reconcile failure.
func (r *BillingAccountReconciler) reconcileDefaultPaymentMethodCondition(
	ctx context.Context,
	account *billingv1alpha1.BillingAccount,
) {
	cond := metav1.Condition{
		Type:               billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady,
		ObservedGeneration: account.Generation,
	}

	switch {
	case account.Spec.DefaultPaymentMethodRef == nil || account.Spec.DefaultPaymentMethodRef.Name == "":
		cond.Status = metav1.ConditionFalse
		cond.Reason = "NotConfigured"
		cond.Message = "No default payment method has been configured for this billing account."

	default:
		var pm billingv1alpha1.PaymentMethod
		key := types.NamespacedName{Namespace: account.Namespace, Name: account.Spec.DefaultPaymentMethodRef.Name}
		err := r.client.Get(ctx, key, &pm)
		switch {
		case apierrors.IsNotFound(err):
			cond.Status = metav1.ConditionFalse
			cond.Reason = "PaymentMethodNotFound"
			cond.Message = fmt.Sprintf("Default payment method %q does not exist in namespace %q.", key.Name, key.Namespace)
		case err != nil:
			cond.Status = metav1.ConditionUnknown
			cond.Reason = "Unknown"
			cond.Message = fmt.Sprintf("Failed to read default payment method %q: %v.", key.Name, err)
		case pm.Status.Phase != billingv1alpha1.PaymentMethodPhaseActive:
			cond.Status = metav1.ConditionFalse
			cond.Reason = "PaymentMethodDegraded"
			cond.Message = fmt.Sprintf("Default payment method %q is in %s phase. Update defaultPaymentMethodRef to an active payment method.", pm.Name, pm.Status.Phase)
		default:
			cond.Status = metav1.ConditionTrue
			cond.Reason = "Ready"
			cond.Message = "Default payment method is active."
		}
	}

	apimeta.SetStatusCondition(&account.Status.Conditions, cond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BillingAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named("billingaccount").
		For(&billingv1alpha1.BillingAccount{}).
		Watches(&billingv1alpha1.BillingAccountBinding{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
					if !ok {
						return nil
					}
					return []reconcile.Request{
						{
							NamespacedName: client.ObjectKey{
								Name:      binding.Spec.BillingAccountRef.Name,
								Namespace: binding.Namespace,
							},
						},
					}
				},
			),
		).
		Watches(&billingv1alpha1.PaymentMethod{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					pm, ok := obj.(*billingv1alpha1.PaymentMethod)
					if !ok {
						return nil
					}
					return []reconcile.Request{{
						NamespacedName: client.ObjectKey{
							Name:      pm.Spec.BillingAccountRef.Name,
							Namespace: pm.Namespace,
						},
					}}
				},
			),
		).
		Complete(r)
}

