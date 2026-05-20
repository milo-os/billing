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

// PaymentMethodReconciler keeps the billing-service-owned state on a
// PaymentMethod consistent. It deliberately does not drive the provider
// flow — that is the provider controller's responsibility (e.g.
// stripe-provider watching for PaymentMethods whose paymentMethodClassRef
// it owns).
//
// Responsibilities:
//   - Set the initial Pending phase when an admitted resource has no
//     status yet.
//   - Surface a "RequiresProvider" condition if no provider controller
//     has taken ownership after the initial reconcile (informational
//     only — diagnostics).
//   - Update ObservedGeneration so consumers can tell when their spec
//     changes have been observed.
type PaymentMethodReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethods,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethodclasses,verbs=get;list;watch

func (r *PaymentMethodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pm billingv1alpha1.PaymentMethod
	if err := r.client.Get(ctx, req.NamespacedName, &pm); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip when the resource is being deleted — let the provider
	// finalizer drive cleanup.
	if !pm.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if pm.Status.Phase == "" {
		base := pm.DeepCopy()
		pm.Status.Phase = billingv1alpha1.PaymentMethodPhasePending
		apimeta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.PaymentMethodConditionInstrumentReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pm.Generation,
			Reason:             "Pending",
			Message:            "Awaiting provider controller to take ownership.",
		})
		pm.Status.ObservedGeneration = pm.Generation
		if err := r.client.Status().Patch(ctx, &pm, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set initial Pending status: %w", err)
		}
		logger.Info("set initial Pending phase", "namespace", pm.Namespace, "name", pm.Name)
		return ctrl.Result{}, nil
	}

	// Mirror provider-driven phase onto the InstrumentReady condition so
	// downstream services only need to read a single signal.
	desired := metav1.Condition{
		Type:               billingv1alpha1.PaymentMethodConditionInstrumentReady,
		ObservedGeneration: pm.Generation,
	}
	switch pm.Status.Phase {
	case billingv1alpha1.PaymentMethodPhaseActive:
		desired.Status = metav1.ConditionTrue
		desired.Reason = "Active"
		desired.Message = "Payment method has been confirmed by the provider."
	case billingv1alpha1.PaymentMethodPhaseFailed:
		desired.Status = metav1.ConditionFalse
		desired.Reason = stringOr(pm.Status.FailureReason, "Failed")
		desired.Message = stringOr(pm.Status.FailureMessage, "Provider reported a setup failure.")
	default:
		desired.Status = metav1.ConditionFalse
		desired.Reason = string(pm.Status.Phase)
		desired.Message = "Provider has not yet confirmed the payment method."
	}

	if conditionMatches(pm.Status.Conditions, desired) && pm.Status.ObservedGeneration == pm.Generation {
		return ctrl.Result{}, nil
	}

	base := pm.DeepCopy()
	apimeta.SetStatusCondition(&pm.Status.Conditions, desired)
	pm.Status.ObservedGeneration = pm.Generation
	if err := r.client.Status().Patch(ctx, &pm, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch InstrumentReady condition: %w", err)
	}
	return ctrl.Result{}, nil
}

func stringOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func conditionMatches(conds []metav1.Condition, target metav1.Condition) bool {
	c := apimeta.FindStatusCondition(conds, target.Type)
	if c == nil {
		return false
	}
	return c.Status == target.Status && c.Reason == target.Reason && c.Message == target.Message
}

// SetupWithManager wires the reconciler into the controller-runtime manager.
func (r *PaymentMethodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("paymentmethod").
		For(&billingv1alpha1.PaymentMethod{}).
		Complete(r)
}
