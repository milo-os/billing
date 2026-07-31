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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
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
	offerAssignable := offerErr == nil && validation.OfferIsAssignable(&offer)

	switch {
	case accountErr != nil:
		reason := "BillingAccountNotFound"
		msg := fmt.Sprintf("BillingAccount %q was not found.", be.Spec.BillingAccountRef.Name)
		if !apierrors.IsNotFound(accountErr) {
			return ctrl.Result{}, fmt.Errorf("getting billing account: %w", accountErr)
		}
		setBEConditions(newStatus, be.Generation, false, reason, msg)

	case !baReady:
		setBEConditions(newStatus, be.Generation, false, "BillingAccountDeleting",
			fmt.Sprintf("BillingAccount %q is being deleted.", account.Name))

	case offerErr != nil:
		if !apierrors.IsNotFound(offerErr) {
			return ctrl.Result{}, fmt.Errorf("getting offer: %w", offerErr)
		}
		setBEConditions(newStatus, be.Generation, false, "OfferNotFound",
			fmt.Sprintf("Offer %q was not found.", be.Spec.OfferRef.Name))

	case offer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA:
		setBEConditions(newStatus, be.Generation, false, "OfferNotGA",
			fmt.Sprintf("Offer %q has launchStage %q; GA is required.", offer.Name, offer.Spec.LaunchStage))

	case !offerAssignable:
		setBEConditions(newStatus, be.Generation, false, "OfferSnapshotPending",
			fmt.Sprintf("Offer %q is GA but has no servicePricings snapshot yet.", offer.Name))

	default:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: be.Generation,
			Reason:             "BillingEntitlementReady",
			Message:            fmt.Sprintf("BillingAccount %q is bound to published Offer %q.", account.Name, offer.Name),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: be.Generation,
			Reason:             "OfferAssigned",
			Message:            fmt.Sprintf("Offer %q is GA with a rate snapshot and assigned.", offer.Name),
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

func setBEConditions(status *billingv1alpha1.BillingEntitlementStatus, generation int64, ready bool, reason, msg string) {
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             readyStatus,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            msg,
	})
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.BillingEntitlementConditionOfferAssigned,
		Status:             readyStatus,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            msg,
	})
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
		Watches(&billingv1alpha1.Offer{},
			handler.EnqueueRequestsFromMapFunc(r.mapOfferToBillingEntitlements),
		).
		Watches(&billingv1alpha1.BillingAccount{},
			handler.EnqueueRequestsFromMapFunc(r.mapBillingAccountToBillingEntitlements),
		).
		Complete(r)
}

func (r *BillingEntitlementReconciler) mapOfferToBillingEntitlements(ctx context.Context, obj client.Object) []reconcile.Request {
	offer, ok := obj.(*billingv1alpha1.Offer)
	if !ok {
		return nil
	}
	var list billingv1alpha1.BillingEntitlementList
	if err := r.client.List(ctx, &list,
		client.MatchingFields{BillingEntitlementOfferRefField: offer.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "listing BillingEntitlements for Offer", "offer", offer.Name)
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: list.Items[i].Namespace,
				Name:      list.Items[i].Name,
			},
		})
	}
	return reqs
}

func (r *BillingEntitlementReconciler) mapBillingAccountToBillingEntitlements(ctx context.Context, obj client.Object) []reconcile.Request {
	account, ok := obj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil
	}
	var list billingv1alpha1.BillingEntitlementList
	if err := r.client.List(ctx, &list,
		client.InNamespace(account.Namespace),
		client.MatchingFields{BillingEntitlementBillingAccountRefField: account.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "listing BillingEntitlements for BillingAccount",
			"billingAccount", account.Name, "namespace", account.Namespace)
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: list.Items[i].Namespace,
				Name:      list.Items[i].Name,
			},
		})
	}
	return reqs
}
