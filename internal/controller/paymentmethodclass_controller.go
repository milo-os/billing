// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// PaymentMethodClassReconciler reconciles a PaymentMethodClass object.
//
// Its job is to resolve spec.parametersRef against the live cluster
// and project the non-sensitive fields the portal needs (currently
// just publishableKey) onto status. This lets consumers read the
// publishable key from PaymentMethodClass.status without ever needing
// IAM access to the provider's own CRD — provider configs stay
// internal to the billing service trust boundary.
type PaymentMethodClassReconciler struct {
	client client.Client
}

// resolveRequeue is how often the controller re-resolves
// parametersRef even when PaymentMethodClass itself hasn't changed.
// Publishable keys change rarely; a one-minute resync is fine and
// avoids the complexity of dynamic informers across arbitrary
// provider API groups.
const resolveRequeue = time.Minute

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethodclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethodclasses/status,verbs=get;update;patch
//
// Provider configuration resources live in sub-groups of
// billing.miloapis.com (e.g. stripe.billing.miloapis.com). The
// reconciler reads them with an unstructured client so billing does
// not have to import any provider types. Kubernetes RBAC does not
// support glob api-group matching, so each known provider group is
// listed explicitly below. Adding a new provider means appending an
// rbac marker for its api-group here.
//
// +kubebuilder:rbac:groups=stripe.billing.miloapis.com,resources=stripeproviderconfigs,verbs=get;list;watch

func (r *PaymentMethodClassReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var class billingv1alpha1.PaymentMethodClass
	if err := r.client.Get(ctx, req.NamespacedName, &class); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	newStatus := class.Status.DeepCopy()
	newStatus.ObservedGeneration = class.Generation

	publishableKey, resolveErr := r.resolvePublishableKey(ctx, class.Spec.ParametersRef)
	if resolveErr != nil {
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.PaymentMethodClassParametersResolved,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: class.Generation,
			Reason:             "ParametersRefUnresolved",
			Message:            resolveErr.Error(),
		})
	} else {
		newStatus.PublishableKey = publishableKey
		apimeta.SetStatusCondition(&newStatus.Conditions, metav1.Condition{
			Type:               billingv1alpha1.PaymentMethodClassParametersResolved,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: class.Generation,
			Reason:             "ParametersRefResolved",
			Message:            fmt.Sprintf("Resolved %s/%s/%s.", class.Spec.ParametersRef.Group, class.Spec.ParametersRef.Kind, class.Spec.ParametersRef.Name),
		})
	}

	if !paymentMethodClassStatusEqual(&class.Status, newStatus) {
		class.Status = *newStatus
		if err := r.client.Status().Update(ctx, &class); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating PaymentMethodClass %q status: %w", class.Name, err)
		}
		logger.Info("reconciled PaymentMethodClass",
			"name", class.Name,
			"provider", class.Spec.Provider,
			"parametersRef", fmt.Sprintf("%s/%s/%s", class.Spec.ParametersRef.Group, class.Spec.ParametersRef.Kind, class.Spec.ParametersRef.Name),
		)
	}

	// Requeue on a steady cadence so we pick up changes to the
	// referenced provider config even when the PaymentMethodClass
	// itself does not change.
	return ctrl.Result{RequeueAfter: resolveRequeue}, nil
}

// resolvePublishableKey fetches the resource pointed to by ref and
// returns its spec.publishableKey. Provider configs are cluster-scoped
// by convention (the existing StripeProviderConfig is); namespaced
// configs are not supported until a real use case appears.
func (r *PaymentMethodClassReconciler) resolvePublishableKey(ctx context.Context, ref billingv1alpha1.PaymentMethodClassParametersRef) (string, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ref.Group,
		Version: "v1alpha1",
		Kind:    ref.Kind,
	})
	if err := r.client.Get(ctx, types.NamespacedName{Name: ref.Name}, u); err != nil {
		return "", fmt.Errorf("getting %s/%s %q: %w", ref.Group, ref.Kind, ref.Name, err)
	}
	publishableKey, found, err := unstructured.NestedString(u.Object, "spec", "publishableKey")
	if err != nil {
		return "", fmt.Errorf("reading spec.publishableKey from %s/%s %q: %w", ref.Group, ref.Kind, ref.Name, err)
	}
	if !found {
		return "", fmt.Errorf("%s/%s %q has no spec.publishableKey", ref.Group, ref.Kind, ref.Name)
	}
	return publishableKey, nil
}

func paymentMethodClassStatusEqual(a, b *billingv1alpha1.PaymentMethodClassStatus) bool {
	if a.PublishableKey != b.PublishableKey {
		return false
	}
	if a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	// Compare just the conditions we manage. Other conditions added by
	// future reconcilers aren't our concern.
	ac := apimeta.FindStatusCondition(a.Conditions, billingv1alpha1.PaymentMethodClassParametersResolved)
	bc := apimeta.FindStatusCondition(b.Conditions, billingv1alpha1.PaymentMethodClassParametersResolved)
	if (ac == nil) != (bc == nil) {
		return false
	}
	if ac != nil && bc != nil {
		if ac.Status != bc.Status || ac.Reason != bc.Reason || ac.ObservedGeneration != bc.ObservedGeneration {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *PaymentMethodClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("billing-paymentmethodclass").
		For(&billingv1alpha1.PaymentMethodClass{}).
		Complete(r)
}
