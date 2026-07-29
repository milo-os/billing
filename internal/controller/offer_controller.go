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

// OfferReconciler reconciles an Offer object.
type OfferReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=servicepricings,verbs=get;list;watch

func (r *OfferReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var offer billingv1alpha1.Offer
	if err := r.client.Get(ctx, req.NamespacedName, &offer); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	newStatus := offer.Status.DeepCopy()
	if newStatus == nil {
		newStatus = &billingv1alpha1.OfferStatus{}
	}
	newStatus.ObservedGeneration = offer.Generation

	switch offer.Spec.LaunchStage {
	case billingv1alpha1.OfferLaunchStageDraft:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: offer.Generation,
			Reason:             "OfferDraft",
			Message:            "Offer is in Draft launch stage and not yet published.",
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: offer.Generation,
			Reason:             "OfferDraft",
			Message:            "Offer has not been published yet.",
		})

	case billingv1alpha1.OfferLaunchStageGA:
		if len(offer.Spec.ServicePricings) == 0 {
			snapshots, err := r.buildSnapshots(ctx, &offer)
			if err != nil {
				apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: offer.Generation,
					Reason:             "ServicePricingResolveFailed",
					Message:            err.Error(),
				})
				apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
					Type:               ConditionTypePublished,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: offer.Generation,
					Reason:             "SnapshotPending",
					Message:            "Waiting for ServicePricing snapshot before the Offer can be published.",
				})
				return r.updateStatusIfNeeded(ctx, &offer, newStatus)
			}

			offer.Spec.ServicePricings = snapshots
			if err := r.client.Update(ctx, &offer); err != nil {
				return ctrl.Result{}, fmt.Errorf("writing servicePricings snapshot: %w", err)
			}
			logger.Info("wrote Offer servicePricings snapshot", "count", len(snapshots))
			return ctrl.Result{}, nil
		}

		if missing := chargeTypesMissingFromSnapshot(&offer); len(missing) > 0 {
			apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: offer.Generation,
				Reason:             "ChargeTypesIncomplete",
				Message:            fmt.Sprintf("chargeTypes is missing %v present in servicePricings snapshot", missing),
			})
			apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
				Type:               ConditionTypePublished,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: offer.Generation,
				Reason:             "ChargeTypesIncomplete",
				Message:            "Offer snapshot is present but chargeTypes does not cover all snapshotted charge types.",
			})
			return r.updateStatusIfNeeded(ctx, &offer, newStatus)
		}

		if newStatus.PublishedAt == nil {
			now := metav1.Now()
			newStatus.PublishedAt = &now
		}

		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: offer.Generation,
			Reason:             "OfferReady",
			Message:            "Offer is GA with a complete servicePricings snapshot.",
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: offer.Generation,
			Reason:             "OfferPublished",
			Message:            "Offer has been published.",
		})

	default:
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: offer.Generation,
			Reason:             "UnknownLaunchStage",
			Message:            fmt.Sprintf("Unknown launchStage %q.", offer.Spec.LaunchStage),
		})
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               ConditionTypePublished,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: offer.Generation,
			Reason:             "UnknownLaunchStage",
			Message:            fmt.Sprintf("Unknown launchStage %q.", offer.Spec.LaunchStage),
		})
	}

	return r.updateStatusIfNeeded(ctx, &offer, newStatus)
}

func (r *OfferReconciler) buildSnapshots(ctx context.Context, offer *billingv1alpha1.Offer) ([]billingv1alpha1.ServicePricingSnapshot, error) {
	if len(offer.Spec.ServicePricingRefs) == 0 {
		return nil, fmt.Errorf("GA Offer has no servicePricingRefs to snapshot")
	}

	snapshots := make([]billingv1alpha1.ServicePricingSnapshot, 0, len(offer.Spec.ServicePricingRefs))
	for _, ref := range offer.Spec.ServicePricingRefs {
		ns := ref.Namespace
		if ns == "" {
			ns = billingv1alpha1.DefaultServicePricingNamespace
		}
		var sp billingv1alpha1.ServicePricing
		if err := r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &sp); err != nil {
			return nil, fmt.Errorf("resolving servicePricingRef %s/%s: %w", ns, ref.Name, err)
		}
		snapshots = append(snapshots, billingv1alpha1.ServicePricingSnapshot{
			Name: sp.Name,
			Spec: *sp.Spec.DeepCopy(),
		})
	}
	return snapshots, nil
}

func chargeTypesMissingFromSnapshot(offer *billingv1alpha1.Offer) []billingv1alpha1.ChargeType {
	declared := make(map[billingv1alpha1.ChargeType]struct{}, len(offer.Spec.ChargeTypes))
	for _, ct := range offer.Spec.ChargeTypes {
		declared[ct] = struct{}{}
	}

	var missing []billingv1alpha1.ChargeType
	seen := make(map[billingv1alpha1.ChargeType]struct{})
	for _, snap := range offer.Spec.ServicePricings {
		ct := snap.Spec.ChargeType
		if ct == "" {
			continue
		}
		if _, ok := declared[ct]; ok {
			continue
		}
		if _, already := seen[ct]; already {
			continue
		}
		seen[ct] = struct{}{}
		missing = append(missing, ct)
	}
	return missing
}

func (r *OfferReconciler) updateStatusIfNeeded(ctx context.Context, offer *billingv1alpha1.Offer, newStatus *billingv1alpha1.OfferStatus) (ctrl.Result, error) {
	if offerStatusEqual(offer.Status, *newStatus) {
		return ctrl.Result{}, nil
	}
	offer.Status = *newStatus
	if err := r.client.Status().Update(ctx, offer); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	return ctrl.Result{}, nil
}

func offerStatusEqual(a, b billingv1alpha1.OfferStatus) bool {
	if a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if (a.PublishedAt == nil) != (b.PublishedAt == nil) {
		return false
	}
	if a.PublishedAt != nil && b.PublishedAt != nil && !a.PublishedAt.Equal(b.PublishedAt) {
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
func (r *OfferReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billing-offer").
		For(&billingv1alpha1.Offer{}).
		Complete(r)
}
