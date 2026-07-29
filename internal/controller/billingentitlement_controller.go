// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// BillingEntitlementReconciler reconciles a BillingEntitlement object.
type BillingEntitlementReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers,verbs=get;list;watch

func (r *BillingEntitlementReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var be billingv1alpha1.BillingEntitlement
	if err := r.client.Get(ctx, req.NamespacedName, &be); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	newStatus := be.Status.DeepCopy()
	if newStatus == nil {
		newStatus = &billingv1alpha1.BillingEntitlementStatus{}
	}
	newStatus.ObservedGeneration = be.Generation

	var account billingv1alpha1.BillingAccount
	accountKey := types.NamespacedName{
		Namespace: be.Namespace,
		Name:      be.Spec.BillingAccountRef.Name,
	}
	accountErr := r.client.Get(ctx, accountKey, &account)

	var offer billingv1alpha1.Offer
	offerErr := r.client.Get(ctx, types.NamespacedName{Name: be.Spec.OfferRef.Name}, &offer)

	baReady := accountErr == nil && account.DeletionTimestamp.IsZero()
	offerGA := offerErr == nil && offer.Spec.LaunchStage == billingv1alpha1.OfferLaunchStageGA

	switch {
	case accountErr != nil:
		reason := "BillingAccountNotFound"
		msg := fmt.Sprintf("BillingAccount %q was not found.", be.Spec.BillingAccountRef.Name)
		if !apierrors.IsNotFound(accountErr) {
			return ctrl.Result{}, fmt.Errorf("getting billing account: %w", accountErr)
		}
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             reason,
			Message:            msg,
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             reason,
			Message:            msg,
		})

	case !baReady:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "BillingAccountDeleting",
			Message:            fmt.Sprintf("BillingAccount %q is being deleted.", account.Name),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "BillingAccountDeleting",
			Message:            fmt.Sprintf("BillingAccount %q is being deleted.", account.Name),
		})

	case offerErr != nil:
		if !apierrors.IsNotFound(offerErr) {
			return ctrl.Result{}, fmt.Errorf("getting offer: %w", offerErr)
		}
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "OfferNotFound",
			Message:            fmt.Sprintf("Offer %q was not found.", be.Spec.OfferRef.Name),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "OfferNotFound",
			Message:            fmt.Sprintf("Offer %q was not found.", be.Spec.OfferRef.Name),
		})

	case !offerGA:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "OfferNotGA",
			Message:            fmt.Sprintf("Offer %q has launchStage %q; GA is required.", offer.Name, offer.Spec.LaunchStage),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: be.Generation,
			Reason:             "OfferNotGA",
			Message:            fmt.Sprintf("Offer %q has launchStage %q; GA is required.", offer.Name, offer.Spec.LaunchStage),
		})

	default:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: be.Generation,
			Reason:             "BillingEntitlementReady",
			Message:            fmt.Sprintf("BillingAccount %q is bound to GA Offer %q.", account.Name, offer.Name),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: be.Generation,
			Reason:             "OfferAssigned",
			Message:            fmt.Sprintf("Offer %q is GA and assigned.", offer.Name),
		})
	}

	if billingEntitlementStatusEqual(be.Status, *newStatus) {
		return ctrl.Result{}, nil
	}
	be.Status = *newStatus
	if err := r.client.Status().Update(ctx, &be); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("reconciled billing entitlement",
		"billingAccount", be.Spec.BillingAccountRef.Name,
		"offer", be.Spec.OfferRef.Name,
	)
	return ctrl.Result{}, nil
}

func billingEntitlementStatusEqual(a, b billingv1alpha1.BillingEntitlementStatus) bool {
	if a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		ac, bc := a.Conditions[i], b.Conditions[i]
		if ac.Type != bc.Type || ac.Status != bc.Status ||
			ac.Reason != bc.Reason || ac.ObservedGeneration != bc.ObservedGeneration ||
			ac.Message != bc.Message {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *BillingEntitlementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billing-billingentitlement").
		For(&billingv1alpha1.BillingEntitlement{}).
		Complete(r)
}
