// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// MonitoredResourceTypeReconciler reconciles a MonitoredResourceType object.
type MonitoredResourceTypeReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=monitoredresourcetypes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=monitoredresourcetypes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=monitoredresourcetypes/finalizers,verbs=update

func (r *MonitoredResourceTypeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var mrt billingv1alpha1.MonitoredResourceType
	if err := r.client.Get(ctx, req.NamespacedName, &mrt); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Compute desired status.
	newStatus := billingv1alpha1.MonitoredResourceTypeStatus{}
	newStatus.ObservedGeneration = mrt.Generation

	// Preserve publishedAt if already set.
	newStatus.PublishedAt = mrt.Status.PublishedAt

	// Set publishedAt when phase first becomes Published.
	if mrt.Spec.Phase == billingv1alpha1.PhasePublished && newStatus.PublishedAt == nil {
		now := metav1.Now()
		newStatus.PublishedAt = &now
	}

	// Carry over existing conditions to mutate them.
	newStatus.Conditions = mrt.Status.Conditions

	// Ready condition: True when phase is not Draft.
	if mrt.Spec.Phase != billingv1alpha1.PhaseDraft {
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mrt.Generation,
			Reason:             "MonitoredResourceTypeReady",
			Message:            fmt.Sprintf("MonitoredResourceType is in %s phase.", mrt.Spec.Phase),
		})
	} else {
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: mrt.Generation,
			Reason:             "MonitoredResourceTypeDraft",
			Message:            "MonitoredResourceType is in Draft phase and not yet published.",
		})
	}

	// Published condition: True when phase is Published, Deprecated, or Retired.
	switch mrt.Spec.Phase {
	case billingv1alpha1.PhasePublished, billingv1alpha1.PhaseDeprecated, billingv1alpha1.PhaseRetired:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: mrt.Generation,
			Reason:             "MonitoredResourceTypePublished",
			Message:            fmt.Sprintf("MonitoredResourceType has been published (current phase: %s).", mrt.Spec.Phase),
		})
	default:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: mrt.Generation,
			Reason:             "MonitoredResourceTypeDraft",
			Message:            "MonitoredResourceType has not been published yet.",
		})
	}

	// Skip status update if nothing changed.
	if statusEqual(mrt.Status.CatalogStatus, newStatus.CatalogStatus) {
		return ctrl.Result{}, nil
	}

	mrt.Status = newStatus
	if err := r.client.Status().Update(ctx, &mrt); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled monitored resource type", "phase", mrt.Spec.Phase)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MonitoredResourceTypeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billing-monitoredresourcetype").
		For(&billingv1alpha1.MonitoredResourceType{}).
		Complete(r)
}
