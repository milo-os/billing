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
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	billingAccountFinalizer = "billing.miloapis.com/billing-account"

	// ConditionTypeReady is the condition type for billing account readiness.
	ConditionTypeReady = "Ready"
)

// BillingAccountReconciler reconciles a BillingAccount object.
type BillingAccountReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings,verbs=get;list;watch

func (r *BillingAccountReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	ctx = mccontext.WithCluster(ctx, req.ClusterName)
	clusterClient := cl.GetClient()

	var account billingv1alpha1.BillingAccount
	if err := clusterClient.Get(ctx, req.NamespacedName, &account); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if !account.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, clusterClient, &account)
	}

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(&account, billingAccountFinalizer) {
		controllerutil.AddFinalizer(&account, billingAccountFinalizer)
		if err := clusterClient.Update(ctx, &account); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Determine the target phase based on current state
	targetPhase := r.determinePhase(&account)

	// Count linked projects
	linkedCount, err := r.countActiveBindings(ctx, clusterClient, &account)
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
		reason := "BillingAccountNotReady"
		message := fmt.Sprintf("Billing account is in %s phase.", targetPhase)
		if targetPhase == billingv1alpha1.BillingAccountPhaseIncomplete {
			reason = "PaymentProfileMissing"
			message = "Billing account requires a payment profile to become ready."
		}
		apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: account.Generation,
			Reason:             reason,
			Message:            message,
		})
	}

	if err := clusterClient.Status().Update(ctx, &account); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled billing account",
		"phase", targetPhase,
		"linkedProjects", linkedCount,
	)

	return ctrl.Result{}, nil
}

// determinePhase computes the target phase based on the account's current
// state and spec.
func (r *BillingAccountReconciler) determinePhase(account *billingv1alpha1.BillingAccount) billingv1alpha1.BillingAccountPhase {
	currentPhase := account.Status.Phase

	// Suspended and Archived are managed externally (e.g., by admin or payment system).
	// The controller does not transition into or out of these states automatically.
	switch currentPhase {
	case billingv1alpha1.BillingAccountPhaseSuspended,
		billingv1alpha1.BillingAccountPhaseArchived:
		return currentPhase
	}

	// For Provisioning, Incomplete, or empty phase: determine based on spec
	if account.Spec.PaymentProfile != nil {
		return billingv1alpha1.BillingAccountPhaseReady
	}

	// No payment profile
	if currentPhase == "" || currentPhase == billingv1alpha1.BillingAccountPhaseProvisioning {
		return billingv1alpha1.BillingAccountPhaseIncomplete
	}

	// If currently Ready but payment profile was removed, transition to Incomplete
	if currentPhase == billingv1alpha1.BillingAccountPhaseReady {
		return billingv1alpha1.BillingAccountPhaseIncomplete
	}

	return billingv1alpha1.BillingAccountPhaseIncomplete
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
func (r *BillingAccountReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		Named("billingaccount").
		For(&billingv1alpha1.BillingAccount{}).
		Watches(&billingv1alpha1.BillingAccountBinding{},
			mchandler.TypedEnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
					if !ok {
						return nil
					}
					return []mcreconcile.Request{
						{
							Request: reconcile.Request{
								NamespacedName: client.ObjectKey{
									Name:      binding.Spec.BillingAccountRef.Name,
									Namespace: binding.Namespace,
								},
							},
						},
					}
				},
			),
		).
		Complete(r)
}
