// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

// BillingArrangementReconciler projects a BillingArrangement's validity
// window onto its status. Phase is derived purely from spec.startsAt,
// spec.endsAt, the stop date of the account's contract for terms tied to
// it, and the current time, so the BillingAccount controller computes the
// same answer with validation.ArrangementPhaseAt instead of waiting on
// this status.
type BillingArrangementReconciler struct {
	client   client.Client
	recorder record.EventRecorder
	now      func() time.Time
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangements,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements,verbs=get;list;watch

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
	contractStop, err := r.contractStop(ctx, &arr)
	if err != nil {
		return ctrl.Result{}, err
	}
	phase := validation.ArrangementPhaseAt(&arr, contractStop, now)
	end := validation.ArrangementEnd(&arr, contractStop)

	base := arr.DeepCopy()
	endedOnTheirOwn := termsEndedOnTheirOwn(base, end, phase)
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
		cond.Message = fmt.Sprintf("Arrangement ended at %s.", end.UTC().Format(time.RFC3339))
		if arr.Spec.EndsWithContract && (arr.Spec.EndsAt == nil || end.Before(arr.Spec.EndsAt)) {
			cond.Message = fmt.Sprintf("Arrangement ended with the contract at %s.", end.UTC().Format(time.RFC3339))
		}
	}
	apimeta.SetStatusCondition(&arr.Status.Conditions, cond)

	if err := r.client.Status().Patch(ctx, &arr, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	if endedOnTheirOwn {
		recordTimelineEvent(r.recorder, &arr, map[string]string{
			billingv1alpha1.EventAnnotationBillingAccount: arr.Spec.BillingAccountRef.Name,
			billingv1alpha1.EventAnnotationEndsAt:         billingv1alpha1.EventTime(end.Time),
		}, corev1.EventTypeNormal, billingv1alpha1.EventReasonPaymentTermsEnded, cond.Message)
	}

	log.FromContext(ctx).V(1).Info("reconciled billing arrangement", "phase", phase)
	return requeueAt(nextArrangementBoundary(&arr, contractStop, now), now), nil
}

// contractStop returns the stop date of the contract on the account the
// arrangement is for, when the terms are tied to it, or the zero time.
func (r *BillingArrangementReconciler) contractStop(ctx context.Context, arr *billingv1alpha1.BillingArrangement) (time.Time, error) {
	if !arr.Spec.EndsWithContract {
		return time.Time{}, nil
	}
	var account billingv1alpha1.BillingAccount
	accountRef := &account
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: arr.Namespace, Name: arr.Spec.BillingAccountRef.Name}, &account); err != nil {
		if !apierrors.IsNotFound(err) {
			return time.Time{}, fmt.Errorf("getting billing account: %w", err)
		}
		accountRef = nil
	}
	return validation.AccountContractStop(ctx, r.client, arr.Namespace, arr.Spec.BillingAccountRef.Name, accountRef)
}

// termsEndedOnTheirOwn reports whether stored arrangement arr, last seen
// Active, has now reached its end without anyone editing it: its endsAt,
// or the contract's stop for terms tied to it. A person ending terms
// changes the spec, so the generation is ahead of the one the status last
// observed; the audit log records that instead.
func termsEndedOnTheirOwn(arr *billingv1alpha1.BillingArrangement, end *metav1.Time, phase billingv1alpha1.BillingArrangementPhase) bool {
	return arr.Status.Phase == billingv1alpha1.BillingArrangementPhaseActive &&
		phase == billingv1alpha1.BillingArrangementPhaseEnded &&
		end != nil &&
		arr.Status.ObservedGeneration == arr.Generation
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
	r.recorder = mgr.GetEventRecorderFor(eventRecorderName) //nolint:staticcheck // SA1019: the events/v1 recorder can't set annotations, which timeline events need.
	return ctrl.NewControllerManagedBy(mgr).
		Named("billingarrangement").
		For(&billingv1alpha1.BillingArrangement{}).
		// Terms tied to the contract end when it stops, so a change to the
		// contract's term moves their end.
		Watches(&billingv1alpha1.BillingEntitlement{},
			handler.EnqueueRequestsFromMapFunc(r.mapEntitlementToTiedArrangements),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Complete(r)
}

// mapEntitlementToTiedArrangements enqueues the account's arrangements that
// end with its contract.
func (r *BillingArrangementReconciler) mapEntitlementToTiedArrangements(ctx context.Context, obj client.Object) []reconcile.Request {
	be, ok := obj.(*billingv1alpha1.BillingEntitlement)
	if !ok {
		return nil
	}
	var list billingv1alpha1.BillingArrangementList
	if err := r.client.List(ctx, &list,
		client.InNamespace(be.Namespace),
		client.MatchingFields{ArrangementBillingAccountRefField: be.Spec.BillingAccountRef.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "listing BillingArrangements for BillingEntitlement", "billingEntitlement", be.Name)
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Spec.EndsWithContract {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

// arrangementStart is when a stored arrangement takes effect.
func arrangementStart(arr *billingv1alpha1.BillingArrangement) time.Time {
	return validation.ArrangementStart(arr, arr.CreationTimestamp.Time)
}

// nextArrangementBoundary returns the next time after now that the
// arrangement's phase can change (its start or its end), or the zero time
// when it never changes again.
func nextArrangementBoundary(arr *billingv1alpha1.BillingArrangement, contractStop, now time.Time) time.Time {
	var next time.Time
	start := arrangementStart(arr)
	end := validation.ArrangementEnd(arr, contractStop)
	var candidates []time.Time
	// Terms withdrawn before they started (an end at or before startsAt)
	// never become active, so their start isn't a boundary.
	if end == nil || start.Before(end.Time) {
		candidates = append(candidates, start)
	}
	if end != nil {
		candidates = append(candidates, end.Time)
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
func pickArrangements(arrs []billingv1alpha1.BillingArrangement, accountUID types.UID, contractStop, now time.Time) accountArrangements {
	var picked accountArrangements
	endsAt := func(a *billingv1alpha1.BillingArrangement) time.Time {
		return validation.ArrangementEnd(a, contractStop).Time
	}
	for i := range arrs {
		arr := &arrs[i]
		if !arr.DeletionTimestamp.IsZero() || validation.OwnedByOtherBillingAccount(arr, accountUID) {
			continue
		}
		switch validation.ArrangementPhaseAt(arr, contractStop, now) {
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

func laterArrangement(a, b *billingv1alpha1.BillingArrangement, at func(*billingv1alpha1.BillingArrangement) time.Time) bool {
	ta, tb := at(a), at(b)
	if ta.Equal(tb) {
		return a.Name > b.Name
	}
	return ta.After(tb)
}
