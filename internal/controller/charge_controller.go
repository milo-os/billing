// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

// ChargeReconciler works out a Charge's amounts and starts it Pending.
// Everything after that (Scheduled, Invoiced, Failed) is the invoicing
// provider's: billing never moves a charge past Pending.
type ChargeReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=charges,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=charges/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=servicepricings,verbs=get;list;watch

// Reconcile writes status.amount, amountDue and listUnitAmount, and the
// first phase.
func (r *ChargeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var charge billingv1alpha1.Charge
	if err := r.client.Get(ctx, req.NamespacedName, &charge); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !charge.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	desired := charge.Status.DeepCopy()
	desired.ObservedGeneration = charge.Generation

	amount, due, err := validation.ChargeAmounts(&charge.Spec)
	if err == nil {
		desired.Amount, desired.AmountDue = amount, due
	}
	desired.ListUnitAmount = ""
	if ref := charge.Spec.ServicePricingRef; ref != nil {
		var pricing billingv1alpha1.ServicePricing
		ns := ref.Namespace
		if ns == "" {
			ns = "milo-system"
		}
		switch getErr := r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &pricing); {
		case getErr == nil:
			if list, ok := chargeListUnitAmount(pricing.Spec.Amount, charge.Spec.UnitAmount); ok {
				desired.ListUnitAmount = list
			}
		case apierrors.IsNotFound(getErr):
			// A pricing deleted since: the charge still bills at its own
			// price, which the spec holds.
		default:
			return ctrl.Result{}, getErr
		}
	}

	if desired.Phase == "" {
		desired.Phase = billingv1alpha1.ChargePhasePending
		reason, msg := "WaitingForProvider", "Waiting for the invoicing provider to put the charge on an invoice"
		if err != nil {
			reason, msg = "Invalid", err.Error()
		}
		apimeta.SetStatusCondition(&desired.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ChargeConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            msg,
			ObservedGeneration: charge.Generation,
		})
	}

	if equalChargeStatus(&charge.Status, desired) {
		return ctrl.Result{}, nil
	}
	patch := client.MergeFrom(charge.DeepCopy())
	charge.Status = *desired
	return ctrl.Result{}, r.client.Status().Patch(ctx, &charge, patch)
}

// chargeListUnitAmount returns the pricing's own amount, with two decimal
// places, when it differs from the charge's.
func chargeListUnitAmount(pricingAmount, unitAmount string) (string, bool) {
	list, ok := validation.CentsString(pricingAmount)
	if !ok {
		return pricingAmount, pricingAmount != "" && pricingAmount != unitAmount
	}
	charged, ok := validation.CentsString(unitAmount)
	return list, !ok || charged != list
}

func equalChargeStatus(a, b *billingv1alpha1.ChargeStatus) bool {
	if a.Phase != b.Phase || a.Amount != b.Amount || a.AmountDue != b.AmountDue ||
		a.ListUnitAmount != b.ListUnitAmount || a.ObservedGeneration != b.ObservedGeneration ||
		len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		x, y := a.Conditions[i], b.Conditions[i]
		if x.Type != y.Type || x.Status != y.Status || x.Reason != y.Reason || x.Message != y.Message {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChargeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("charge").
		For(&billingv1alpha1.Charge{}).
		Complete(r)
}
