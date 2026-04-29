// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	billingAccountFinalizer = "billing.miloapis.com/billing-account"
)

// BillingAccountReconciler reconciles a BillingAccount object.
type BillingAccountReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings,verbs=get;list;watch

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

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(&account, billingAccountFinalizer) {
		controllerutil.AddFinalizer(&account, billingAccountFinalizer)
		if err := r.client.Update(ctx, &account); err != nil {
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

	// Remove finalizer
	controllerutil.RemoveFinalizer(account, billingAccountFinalizer)
	if err := cl.Update(ctx, account); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	logger.Info("finalized billing account")
	return ctrl.Result{}, nil
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
		Complete(r)
}
