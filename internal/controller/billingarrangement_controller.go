// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

// BillingArrangementReconciler projects a BillingArrangement's validity
// window onto its status. Phase is derived purely from spec.startsAt,
// spec.endsAt and the current time, so the BillingAccount controller
// computes the same answer with validation.ArrangementPhaseAt instead of
// waiting on this status.
type BillingArrangementReconciler struct {
	client client.Client
	now    func() time.Time
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangements,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangements/status,verbs=get;update;patch

func (r *BillingArrangementReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var arr billingv1alpha1.BillingArrangement
	if err := r.client.Get(ctx, req.NamespacedName, &arr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !arr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	now := nowFrom(r.now)
	phase := validation.ArrangementPhaseAt(&arr, now)

	base := arr.DeepCopy()
	arr.Status.Phase = phase
	arr.Status.ObservedGeneration = arr.Generation

	cond := metav1.Condition{
		Type:               billingv1alpha1.BillingArrangementConditionActive,
		ObservedGeneration: arr.Generation,
		Reason:             string(phase),
	}
	switch phase {
	case billingv1alpha1.BillingArrangementPhaseActive:
		cond.Status = metav1.ConditionTrue
		cond.Message = "Arrangement is in effect."
	case billingv1alpha1.BillingArrangementPhaseScheduled:
		cond.Status = metav1.ConditionFalse
		cond.Message = fmt.Sprintf("Arrangement takes effect at %s.", arrangementStart(&arr).UTC().Format(time.RFC3339))
	default:
		cond.Status = metav1.ConditionFalse
		cond.Message = fmt.Sprintf("Arrangement ended at %s.", arr.Spec.EndsAt.UTC().Format(time.RFC3339))
	}
	apimeta.SetStatusCondition(&arr.Status.Conditions, cond)

	if err := r.client.Status().Patch(ctx, &arr, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	log.FromContext(ctx).V(1).Info("reconciled billing arrangement", "phase", phase)
	return requeueAt(nextArrangementBoundary(&arr, now), now), nil
}

// nowFrom returns the reconciler's injected clock reading, or the wall
// clock when none was injected.
func nowFrom(clock func() time.Time) time.Time {
	if clock != nil {
		return clock()
	}
	return time.Now()
}

// SetupWithManager sets up the controller with the Manager.
func (r *BillingArrangementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billingarrangement").
		For(&billingv1alpha1.BillingArrangement{}).
		Complete(r)
}

// arrangementStart is when a stored arrangement takes effect.
func arrangementStart(arr *billingv1alpha1.BillingArrangement) time.Time {
	return validation.ArrangementStart(arr, arr.CreationTimestamp.Time)
}

// nextArrangementBoundary returns the next time after now that the
// arrangement's phase can change (its start or its end), or the zero time
// when it never changes again.
func nextArrangementBoundary(arr *billingv1alpha1.BillingArrangement, now time.Time) time.Time {
	var next time.Time
	start := arrangementStart(arr)
	var candidates []time.Time
	// Terms withdrawn before they started (endsAt at or before startsAt)
	// never become active, so their start isn't a boundary.
	if arr.Spec.EndsAt == nil || start.Before(arr.Spec.EndsAt.Time) {
		candidates = append(candidates, start)
	}
	if arr.Spec.EndsAt != nil {
		candidates = append(candidates, arr.Spec.EndsAt.Time)
	}
	for _, t := range candidates {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

// requeueAt converts a wall-clock time into a reconcile result. A small
// margin past the boundary keeps the next reconcile from landing a hair
// early and seeing the old phase.
func requeueAt(at, now time.Time) ctrl.Result {
	if at.IsZero() {
		return ctrl.Result{}
	}
	return ctrl.Result{RequeueAfter: at.Sub(now) + time.Second}
}

// accountArrangements is what an account's arrangements mean at a point
// in time.
type accountArrangements struct {
	// active is the arrangement in effect, if any.
	active *billingv1alpha1.BillingArrangement
	// lastEnded is the arrangement that ended most recently.
	lastEnded *billingv1alpha1.BillingArrangement
	// nextScheduled is the arrangement that starts soonest.
	nextScheduled *billingv1alpha1.BillingArrangement
}

// pickArrangements classifies an account's arrangements at now. The
// webhook prevents overlapping windows, but if two slip through the
// later start wins, then the greater name, so the choice is
// deterministic.
//
// Arrangements owned by a different BillingAccount UID are skipped: they
// belonged to a deleted account with the same name and are waiting for
// garbage collection. accountUID may be empty (in unit tests), which
// skips nothing.
func pickArrangements(arrs []billingv1alpha1.BillingArrangement, accountUID types.UID, now time.Time) accountArrangements {
	var picked accountArrangements
	endsAt := func(a *billingv1alpha1.BillingArrangement) time.Time { return a.Spec.EndsAt.Time }
	for i := range arrs {
		arr := &arrs[i]
		if !arr.DeletionTimestamp.IsZero() || ownedByOtherAccount(arr, accountUID) {
			continue
		}
		switch validation.ArrangementPhaseAt(arr, now) {
		case billingv1alpha1.BillingArrangementPhaseActive:
			if picked.active == nil || laterArrangement(arr, picked.active, arrangementStart) {
				picked.active = arr
			}
		case billingv1alpha1.BillingArrangementPhaseEnded:
			if picked.lastEnded == nil || laterArrangement(arr, picked.lastEnded, endsAt) {
				picked.lastEnded = arr
			}
		case billingv1alpha1.BillingArrangementPhaseScheduled:
			if picked.nextScheduled == nil || laterArrangement(picked.nextScheduled, arr, arrangementStart) {
				picked.nextScheduled = arr
			}
		}
	}
	return picked
}

func ownedByOtherAccount(arr *billingv1alpha1.BillingArrangement, accountUID types.UID) bool {
	if accountUID == "" {
		return false
	}
	for _, ref := range arr.OwnerReferences {
		if ref.Kind == "BillingAccount" && ref.UID != accountUID {
			return true
		}
	}
	return false
}

func laterArrangement(a, b *billingv1alpha1.BillingArrangement, at func(*billingv1alpha1.BillingArrangement) time.Time) bool {
	ta, tb := at(a), at(b)
	if ta.Equal(tb) {
		return a.Name > b.Name
	}
	return ta.After(tb)
}
