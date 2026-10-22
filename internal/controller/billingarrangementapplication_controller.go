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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

// BillingArrangementApplicationReconciler projects the staff decision on
// a payment terms application onto its status: the arrangement that
// approved it, or the customer-facing message from the decline. The
// decline's internal notes are never copied.
type BillingArrangementApplicationReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangementapplications,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangementapplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangementdeclines,verbs=get;list;watch

func (r *BillingArrangementApplicationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var app billingv1alpha1.BillingArrangementApplication
	if err := r.client.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !app.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var arrList billingv1alpha1.BillingArrangementList
	if err := r.client.List(ctx, &arrList,
		client.InNamespace(app.Namespace),
		client.MatchingFields{ArrangementApplicationRefField: app.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing billing arrangements: %w", err)
	}
	var declineList billingv1alpha1.BillingArrangementDeclineList
	if err := r.client.List(ctx, &declineList,
		client.InNamespace(app.Namespace),
		client.MatchingFields{DeclineApplicationRefField: app.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing billing arrangement declines: %w", err)
	}

	base := app.DeepCopy()
	setApplicationStatus(&app, validation.FindApplicationDecision(&app, arrList.Items, declineList.Items))

	if err := r.client.Status().Patch(ctx, &app, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	log.FromContext(ctx).V(1).Info("reconciled billing arrangement application", "phase", app.Status.Phase)
	return ctrl.Result{}, nil
}

// setApplicationStatus writes the phase and decision details for d onto
// the application's status.
func setApplicationStatus(app *billingv1alpha1.BillingArrangementApplication, d validation.ApplicationDecision) {
	phase := validation.ApplicationPhase(app, d)
	app.Status.Phase = phase
	app.Status.ObservedGeneration = app.Generation
	app.Status.DecidedAt = nil
	app.Status.ArrangementRef = nil
	app.Status.DeclineMessage = ""

	cond := metav1.Condition{
		Type:               billingv1alpha1.BillingArrangementApplicationConditionDecided,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: app.Generation,
		Reason:             string(phase),
	}
	switch phase {
	case billingv1alpha1.BillingArrangementApplicationPhaseApproved:
		decidedAt := d.Arrangement.CreationTimestamp
		app.Status.DecidedAt = &decidedAt
		app.Status.ArrangementRef = &billingv1alpha1.BillingArrangementRef{Name: d.Arrangement.Name}
		cond.Message = fmt.Sprintf("Approved with arrangement %q.", d.Arrangement.Name)
	case billingv1alpha1.BillingArrangementApplicationPhaseDeclined:
		decidedAt := d.Decline.CreationTimestamp
		app.Status.DecidedAt = &decidedAt
		app.Status.DeclineMessage = d.Decline.Spec.Message
		cond.Message = "The application was declined."
	case billingv1alpha1.BillingArrangementApplicationPhaseWithdrawn:
		cond.Message = "The applicant withdrew the application."
	default:
		cond.Status = metav1.ConditionFalse
		cond.Message = "The application is awaiting review."
	}
	apimeta.SetStatusCondition(&app.Status.Conditions, cond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BillingArrangementApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billingarrangementapplication").
		For(&billingv1alpha1.BillingArrangementApplication{}).
		Watches(&billingv1alpha1.BillingArrangement{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					arr, ok := obj.(*billingv1alpha1.BillingArrangement)
					if !ok || arr.Spec.ApplicationRef == nil {
						return nil
					}
					return []reconcile.Request{{
						NamespacedName: client.ObjectKey{Name: arr.Spec.ApplicationRef.Name, Namespace: arr.Namespace},
					}}
				},
			),
		).
		Watches(&billingv1alpha1.BillingArrangementDecline{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					decline, ok := obj.(*billingv1alpha1.BillingArrangementDecline)
					if !ok {
						return nil
					}
					return []reconcile.Request{{
						NamespacedName: client.ObjectKey{Name: decline.Spec.ApplicationRef.Name, Namespace: decline.Namespace},
					}}
				},
			),
		).
		Complete(r)
}
