// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// testBillingAccountReconciler is a test adapter for envtest that exercises
// the same logic as the real BillingAccountReconciler while adding
// test-only behaviors (e.g., refetch-before-update) where appropriate.
type testBillingAccountReconciler struct {
	client client.Client
}

func (r *testBillingAccountReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var account billingv1alpha1.BillingAccount
	if err := r.client.Get(ctx, req.NamespacedName, &account); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if !account.DeletionTimestamp.IsZero() {
		activeCount, err := r.countActiveBindings(ctx, &account)
		if err != nil {
			return ctrl.Result{}, err
		}
		if activeCount > 0 {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		controllerutil.RemoveFinalizer(&account, billingAccountFinalizer)
		return ctrl.Result{}, r.client.Update(ctx, &account)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&account, billingAccountFinalizer) {
		controllerutil.AddFinalizer(&account, billingAccountFinalizer)
		if err := r.client.Update(ctx, &account); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Determine phase
	reconciler := &BillingAccountReconciler{}
	targetPhase := reconciler.determinePhase(&account)

	linkedCount, err := r.countActiveBindings(ctx, &account)
	if err != nil {
		return ctrl.Result{}, err
	}

	account.Status.Phase = targetPhase
	account.Status.LinkedProjectsCount = linkedCount
	account.Status.ObservedGeneration = account.Generation

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

	// Mirror the production controller's default-payment-method
	// projection so envtests exercise the same code path.
	prod := &BillingAccountReconciler{client: r.client}
	prod.reconcileDefaultPaymentMethodCondition(ctx, &account)
	if err := prod.reconcileInvoicingCondition(ctx, &account); err != nil {
		return ctrl.Result{}, err
	}

	desiredStatus := account.Status.DeepCopy()

	// Refetch before status write so concurrent watches (PaymentMethod,
	// Binding, Invoice) do not lose to stale ResourceVersions.
	var latest billingv1alpha1.BillingAccount
	if err := r.client.Get(ctx, req.NamespacedName, &latest); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	latest.Status = *desiredStatus
	if err := r.client.Status().Update(ctx, &latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// testPaymentMethodReconciler is a test adapter that mirrors the
// production PaymentMethodReconciler: it sets Pending phase on newly
// admitted resources and projects the InstrumentReady condition from
// status.phase. It is wired in suite_test.go so envtests that mutate
// PaymentMethod status see the same condition lifecycle as production.
type testPaymentMethodReconciler struct {
	client client.Client
}

func (r *testPaymentMethodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var pm billingv1alpha1.PaymentMethod
	if err := r.client.Get(ctx, req.NamespacedName, &pm); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !pm.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	base := pm.DeepCopy()
	if pm.Status.Phase == "" {
		pm.Status.Phase = billingv1alpha1.PaymentMethodPhasePending
	}
	cond := metav1.Condition{
		Type:               billingv1alpha1.PaymentMethodConditionInstrumentReady,
		ObservedGeneration: pm.Generation,
	}
	switch pm.Status.Phase {
	case billingv1alpha1.PaymentMethodPhaseActive:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Active"
		cond.Message = "Payment method has been confirmed by the provider."
	case billingv1alpha1.PaymentMethodPhaseFailed:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "Failed"
		cond.Message = "Provider reported a setup failure."
	default:
		cond.Status = metav1.ConditionFalse
		cond.Reason = string(pm.Status.Phase)
		cond.Message = "Provider has not yet confirmed the payment method."
	}
	apimeta.SetStatusCondition(&pm.Status.Conditions, cond)
	pm.Status.ObservedGeneration = pm.Generation
	return ctrl.Result{}, r.client.Status().Patch(ctx, &pm, client.MergeFrom(base))
}

// reconcileAccountFromPaymentMethod enqueues the referenced
// BillingAccount when a PaymentMethod changes.
func reconcileAccountFromPaymentMethod(cl client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			pm, ok := obj.(*billingv1alpha1.PaymentMethod)
			if !ok {
				return nil
			}
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Name:      pm.Spec.BillingAccountRef.Name,
					Namespace: pm.Namespace,
				},
			}}
		},
	)
}

// reconcileAccountFromInvoice enqueues the referenced BillingAccount
// when an Invoice changes.
func reconcileAccountFromInvoice(cl client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			invoice, ok := obj.(*billingv1alpha1.Invoice)
			if !ok {
				return nil
			}
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Name:      invoice.Spec.BillingAccountRef.Name,
					Namespace: invoice.Namespace,
				},
			}}
		},
	)
}

func (r *testBillingAccountReconciler) countActiveBindings(ctx context.Context, account *billingv1alpha1.BillingAccount) (int32, error) {
	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList,
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

// testBillingAccountBindingReconciler is a test adapter for envtest.
type testBillingAccountBindingReconciler struct {
	client        client.Client
	projectReader client.Reader
}

func (r *testBillingAccountBindingReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var binding billingv1alpha1.BillingAccountBinding
	if err := r.client.Get(ctx, req.NamespacedName, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !binding.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Mirrors BillingAccountBindingReconciler.Reconcile's project check. Keep
	// the two in sync: this adapter doesn't call the production reconciler,
	// so drift here means a passing test that doesn't prove anything.
	var project resourcemanagerv1alpha1.Project
	err := r.projectReader.Get(ctx, client.ObjectKey{Name: binding.Spec.ProjectRef.Name}, &project)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if apierrors.IsNotFound(err) || !project.DeletionTimestamp.IsZero() {
		if err := r.client.Delete(ctx, &binding); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseSuperseded {
		return ctrl.Result{}, nil
	}

	// Set billing responsibility
	if binding.Status.BillingResponsibility == nil {
		binding.Status.BillingResponsibility = &billingv1alpha1.BillingResponsibility{}
	}
	if binding.Status.BillingResponsibility.EstablishedAt == nil {
		now := metav1.Now()
		binding.Status.BillingResponsibility.EstablishedAt = &now
	}
	binding.Status.BillingResponsibility.CurrentAccount = binding.Spec.BillingAccountRef.Name

	// Supersede older bindings
	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList,
		client.InNamespace(binding.Namespace),
		client.MatchingFields{BindingProjectRefField: binding.Spec.ProjectRef.Name},
	); err != nil {
		return ctrl.Result{}, err
	}

	sort.Slice(bindingList.Items, func(i, j int) bool {
		ti := bindingList.Items[i].CreationTimestamp
		tj := bindingList.Items[j].CreationTimestamp
		if ti.Equal(&tj) {
			return bindingList.Items[i].Name < bindingList.Items[j].Name
		}
		return ti.Before(&tj)
	})

	for i := range bindingList.Items {
		other := &bindingList.Items[i]
		if other.UID == binding.UID {
			continue
		}
		if other.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseSuperseded {
			continue
		}
		isOlder := other.CreationTimestamp.Before(&binding.CreationTimestamp) ||
			(other.CreationTimestamp.Equal(&binding.CreationTimestamp) && other.Name < binding.Name)
		if !isOlder {
			continue
		}
		// Refetch to get latest resource version before updating status
		var fresh billingv1alpha1.BillingAccountBinding
		if err := r.client.Get(ctx, client.ObjectKeyFromObject(other), &fresh); err != nil {
			return ctrl.Result{}, err
		}
		if fresh.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseSuperseded {
			continue
		}
		fresh.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
		apimeta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeBound,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: fresh.Generation,
			Reason:             "Superseded",
			Message:            fmt.Sprintf("Superseded by binding %s.", binding.Name),
		})
		if err := r.client.Status().Update(ctx, &fresh); err != nil {
			return ctrl.Result{}, err
		}
	}

	binding.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseActive
	binding.Status.ObservedGeneration = binding.Generation
	apimeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeBound,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: binding.Generation,
		Reason:             "SuccessfulBinding",
		Message:            "Project successfully bound to billing account.",
	})

	return ctrl.Result{}, r.client.Status().Update(ctx, &binding)
}

// requestsForProject mirrors BillingAccountBindingReconciler.bindingRequestsForProject.
func (r *testBillingAccountBindingReconciler) requestsForProject(ctx context.Context, obj client.Object) []reconcile.Request {
	project, ok := obj.(*resourcemanagerv1alpha1.Project)
	if !ok {
		return nil
	}

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := r.client.List(ctx, &bindingList,
		client.MatchingFields{BindingProjectRefField: project.Name},
	); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(bindingList.Items))
	for i := range bindingList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&bindingList.Items[i]),
		})
	}
	return requests
}

// reconcileAccountFromBinding returns an event handler that enqueues the
// referenced BillingAccount when a BillingAccountBinding changes.
func reconcileAccountFromBinding(cl client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			binding, ok := obj.(*billingv1alpha1.BillingAccountBinding)
			if !ok {
				return nil
			}
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      binding.Spec.BillingAccountRef.Name,
						Namespace: binding.Namespace,
					},
				},
			}
		},
	)
}
