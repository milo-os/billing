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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// meterDefinitionFinalizer is placed on every MeterDefinition so that
// future pricing / entitlement / usage resources can rely on the meter
// remaining present while they hold a reference. For v0 there are no
// downstream resources to check, so the finalizer is removed immediately
// on deletion.
const meterDefinitionFinalizer = "billing.miloapis.com/meter-definition"

const (
	// reasonMeterDefinitionActive is the Ready=True reason.
	reasonMeterDefinitionActive = "MeterDefinitionActive"

	// reasonDuplicateMeterName is the Ready=False reason when another
	// MeterDefinition with the same spec.meterName exists. The webhook
	// prevents the common case; this condition is how the controller
	// surfaces the race between two concurrent admissions.
	reasonDuplicateMeterName = "DuplicateMeterName"
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

	if !md.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &md)
	}

	if !controllerutil.ContainsFinalizer(&md, meterDefinitionFinalizer) {
		controllerutil.AddFinalizer(&md, meterDefinitionFinalizer)
		if err := r.client.Update(ctx, &md); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	readyCondition, err := r.desiredReadyCondition(ctx, &md)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !statusNeedsUpdate(&md, readyCondition) {
		return ctrl.Result{}, nil
	}

	md.Status.ObservedGeneration = md.Generation
	apimeta.SetStatusCondition(&md.Status.Conditions, readyCondition)

	if err := r.client.Status().Update(ctx, &md); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled meter definition",
		"meterName", md.Spec.MeterName,
		"owner", md.Spec.Owner.Service,
		"ready", readyCondition.Status,
	)

	return ctrl.Result{}, nil
}

// desiredReadyCondition builds the Ready condition for this reconcile.
// The webhook rejects duplicate meterNames at admission, but two
// concurrent creates can slip past the check. If this controller ever
// observes a duplicate, every offender is marked Ready=False so the
// collision is visible via `kubectl get` and to downstream automations.
func (r *MeterDefinitionReconciler) desiredReadyCondition(
	ctx context.Context,
	md *billingv1alpha1.MeterDefinition,
) (metav1.Condition, error) {
	var list billingv1alpha1.MeterDefinitionList
	if err := r.client.List(ctx, &list,
		client.MatchingFields{MeterDefinitionMeterNameField: md.Spec.MeterName},
	); err != nil {
		return metav1.Condition{}, fmt.Errorf("failed to list duplicates for %q: %w", md.Spec.MeterName, err)
	}

	for i := range list.Items {
		other := &list.Items[i]
		if other.UID == md.UID {
			continue
		}
		return metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: md.Generation,
			Reason:             reasonDuplicateMeterName,
			Message: fmt.Sprintf("meterName %q is also defined by %q; resolve the collision before use",
				md.Spec.MeterName, other.Name),
		}, nil
	}

	return metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: md.Generation,
		Reason:             reasonMeterDefinitionActive,
		Message:            "Meter definition is active and available to downstream systems.",
	}, nil
}

// statusNeedsUpdate returns true when the observed generation is stale or
// the existing Ready condition does not match the desired one. Avoids
// write amplification on resources that are already settled.
func statusNeedsUpdate(md *billingv1alpha1.MeterDefinition, desired metav1.Condition) bool {
	if md.Status.ObservedGeneration != md.Generation {
		return true
	}
	existing := apimeta.FindStatusCondition(md.Status.Conditions, ConditionTypeReady)
	if existing == nil {
		return true
	}
	return existing.Status != desired.Status ||
		existing.Reason != desired.Reason ||
		existing.Message != desired.Message ||
		existing.ObservedGeneration != desired.ObservedGeneration
}

// reconcileDelete handles the deletion of a MeterDefinition. With no
// downstream references to check in v0, the finalizer is removed
// immediately. The finalizer is still installed so that downstream
// reference checks can be added later without an API change.
func (r *MeterDefinitionReconciler) reconcileDelete(
	ctx context.Context,
	md *billingv1alpha1.MeterDefinition,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(md, meterDefinitionFinalizer) {
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(md, meterDefinitionFinalizer)
	if err := r.client.Update(ctx, md); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	logger.Info("finalized meter definition", "meterName", md.Spec.MeterName)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MeterDefinitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named("meterdefinition").
		For(&billingv1alpha1.MeterDefinition{}).
		Complete(r)
}
