// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// ConditionTypeBound is the condition type for binding status.
	ConditionTypeBound = "Bound"
)

// BillingAccountBindingReconciler reconciles a BillingAccountBinding object.
type BillingAccountBindingReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccountbindings/finalizers,verbs=update

func (r *BillingAccountBindingReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var binding billingv1alpha1.BillingAccountBinding
	if err := r.client.Get(ctx, req.NamespacedName, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if being deleted or already superseded
	if !binding.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseSuperseded {
		return ctrl.Result{}, nil
	}

	// Set billing responsibility if not already set
	if binding.Status.BillingResponsibility == nil {
		binding.Status.BillingResponsibility = &billingv1alpha1.BillingResponsibility{}
	}
	if binding.Status.BillingResponsibility.EstablishedAt == nil {
		now := metav1.Now()
		binding.Status.BillingResponsibility.EstablishedAt = &now
	}
	binding.Status.BillingResponsibility.CurrentAccount = binding.Spec.BillingAccountRef.Name

	// Find and supersede older active bindings for the same project
	if err := r.supersedeOlderBindings(ctx, r.client, &binding); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to supersede older bindings: %w", err)
	}

	// Set phase and condition
	binding.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseActive
	binding.Status.ObservedGeneration = binding.Generation

	apimeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeBound,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: binding.Generation,
		Reason:             "SuccessfulBinding",
		Message:            "Project successfully bound to billing account.",
	})

	if err := r.client.Status().Update(ctx, &binding); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update binding status: %w", err)
	}

	logger.Info("reconciled billing account binding",
		"project", binding.Spec.ProjectRef.Name,
		"account", binding.Spec.BillingAccountRef.Name,
		"phase", binding.Status.Phase,
	)

	return ctrl.Result{}, nil
}

// supersedeOlderBindings finds other active bindings for the same project
// and marks them as superseded.
func (r *BillingAccountBindingReconciler) supersedeOlderBindings(
	ctx context.Context,
	cl client.Client,
	currentBinding *billingv1alpha1.BillingAccountBinding,
) error {
	logger := log.FromContext(ctx)

	var bindingList billingv1alpha1.BillingAccountBindingList
	if err := cl.List(ctx, &bindingList,
		client.InNamespace(currentBinding.Namespace),
		client.MatchingFields{BindingProjectRefField: currentBinding.Spec.ProjectRef.Name},
	); err != nil {
		return err
	}

	// Sort by creation timestamp (oldest first), then by name for deterministic
	// tie-breaking when timestamps are equal (e.g., concurrent creation).
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

		// Skip self
		if other.UID == currentBinding.UID {
			continue
		}

		// Skip already superseded
		if other.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseSuperseded {
			continue
		}

		// Only supersede bindings that are strictly older than the current one.
		// Use deterministic ordering: older timestamp wins, ties broken by name.
		isOlder := other.CreationTimestamp.Before(&currentBinding.CreationTimestamp) ||
			(other.CreationTimestamp.Equal(&currentBinding.CreationTimestamp) &&
				other.Name < currentBinding.Name)
		if !isOlder {
			continue
		}

		// Mark as superseded
		other.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
		apimeta.SetStatusCondition(&other.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeBound,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: other.Generation,
			Reason:             "Superseded",
			Message:            fmt.Sprintf("Superseded by binding %s.", currentBinding.Name),
		})

		if err := cl.Status().Update(ctx, other); err != nil {
			return fmt.Errorf("failed to supersede binding %s: %w", other.Name, err)
		}

		logger.Info("superseded older binding",
			"superseded", other.Name,
			"by", currentBinding.Name,
		)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BillingAccountBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named("billingaccountbinding").
		For(&billingv1alpha1.BillingAccountBinding{}).
		Complete(r)
}
