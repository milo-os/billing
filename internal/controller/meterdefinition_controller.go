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

// MeterDefinitionReconciler reconciles a MeterDefinition object.
type MeterDefinitionReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=meterdefinitions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=meterdefinitions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=meterdefinitions/finalizers,verbs=update

func (r *MeterDefinitionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var md billingv1alpha1.MeterDefinition
	if err := r.client.Get(ctx, req.NamespacedName, &md); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Compute desired status.
	newStatus := billingv1alpha1.MeterDefinitionStatus{}
	newStatus.ObservedGeneration = md.Generation

	// Preserve publishedAt if already set.
	newStatus.PublishedAt = md.Status.PublishedAt

	// Set publishedAt when phase first becomes Published.
	if md.Spec.Phase == billingv1alpha1.PhasePublished && newStatus.PublishedAt == nil {
		now := metav1.Now()
		newStatus.PublishedAt = &now
	}

	// Carry over existing conditions to mutate them.
	newStatus.Conditions = md.Status.Conditions

	// Ready condition: True when phase is not Draft.
	if md.Spec.Phase != billingv1alpha1.PhaseDraft {
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: md.Generation,
			Reason:             "MeterDefinitionReady",
			Message:            fmt.Sprintf("MeterDefinition is in %s phase.", md.Spec.Phase),
		})
	} else {
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: md.Generation,
			Reason:             "MeterDefinitionDraft",
			Message:            "MeterDefinition is in Draft phase and not yet published.",
		})
	}

	// Published condition: True when phase is Published, Deprecated, or Retired.
	switch md.Spec.Phase {
	case billingv1alpha1.PhasePublished, billingv1alpha1.PhaseDeprecated, billingv1alpha1.PhaseRetired:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: md.Generation,
			Reason:             "MeterDefinitionPublished",
			Message:            fmt.Sprintf("MeterDefinition has been published (current phase: %s).", md.Spec.Phase),
		})
	default:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: md.Generation,
			Reason:             "MeterDefinitionDraft",
			Message:            "MeterDefinition has not been published yet.",
		})
	}

	// Skip status update if nothing changed.
	if statusEqual(md.Status.CatalogStatus, newStatus.CatalogStatus) {
		return ctrl.Result{}, nil
	}

	md.Status = newStatus
	if err := r.client.Status().Update(ctx, &md); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled meter definition", "phase", md.Spec.Phase)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MeterDefinitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billing-meterdefinition").
		For(&billingv1alpha1.MeterDefinition{}).
		Complete(r)
}
