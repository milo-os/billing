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
	stripeprovider "go.miloapis.com/billing/internal/payment/stripe"
)

// PaymentMethodSetupReconciler reconciles PaymentMethodSetup resources by
// driving an upstream payment-processor SetupIntent and writing the
// resulting clientSecret back to status. Today only the Stripe provider is
// supported; the type-switch on PaymentProvider.spec.type is the extension
// point when other providers are added.
type PaymentMethodSetupReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethodsetups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethodsetups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *PaymentMethodSetupReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var setup billingv1alpha1.PaymentMethodSetup
	if err := r.client.Get(ctx, req.NamespacedName, &setup); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Terminal phases are sticky.
	switch setup.Status.Phase {
	case billingv1alpha1.PaymentMethodSetupPhaseSucceeded,
		billingv1alpha1.PaymentMethodSetupPhaseFailed:
		return ctrl.Result{}, nil
	}

	// Look up the BillingAccount this setup belongs to.
	var account billingv1alpha1.BillingAccount
	accKey := types.NamespacedName{Namespace: setup.Namespace, Name: setup.Spec.BillingAccountRef.Name}
	if err := r.client.Get(ctx, accKey, &account); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.markFailed(ctx, &setup, "BillingAccountNotFound",
				fmt.Sprintf("BillingAccount %s/%s not found.", accKey.Namespace, accKey.Name))
		}
		return ctrl.Result{}, fmt.Errorf("getting BillingAccount: %w", err)
	}

	if account.Spec.PaymentProviderRef == nil || account.Spec.PaymentProviderRef.Name == "" {
		return ctrl.Result{}, r.markFailed(ctx, &setup, "NoPaymentProvider",
			"BillingAccount has no spec.paymentProviderRef.")
	}

	cfg, err := stripeprovider.ResolveProviderConfig(ctx, r.client, account.Spec.PaymentProviderRef.Name)
	if err != nil {
		// Resolving config is environmental — retry rather than fail.
		return ctrl.Result{}, fmt.Errorf("resolving PaymentProvider %q: %w", account.Spec.PaymentProviderRef.Name, err)
	}

	// If the SetupIntent has already been created upstream we have
	// nothing more to do until the webhook fires.
	if setup.Status.SetupIntentID != "" && setup.Status.ClientSecret != "" {
		return ctrl.Result{}, nil
	}

	stripe := stripeprovider.NewClient(cfg)

	// Reuse a previously recorded Stripe Customer when present;
	// otherwise create one.
	var existingCustomer string
	if account.Status.PaymentMethod != nil {
		existingCustomer = account.Status.PaymentMethod.ProviderCustomerID
	}
	customerEmail := ""
	if account.Spec.ContactInfo != nil {
		customerEmail = account.Spec.ContactInfo.Email
	}
	customerID, err := stripe.EnsureCustomer(ctx, existingCustomer, account.Name, customerEmail)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Stripe customer: %w", err)
	}

	siID, clientSecret, siStatus, err := stripe.CreateSetupIntent(ctx,
		customerID,
		fmt.Sprintf("%s/%s", setup.Namespace, setup.Name),
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating Stripe SetupIntent: %w", err)
	}

	// Patch BillingAccount.status with the customer id so subsequent
	// setups reuse it.
	if account.Status.PaymentMethod == nil {
		account.Status.PaymentMethod = &billingv1alpha1.PaymentMethodInfo{}
	}
	if account.Status.PaymentMethod.ProviderCustomerID == "" {
		patch := client.MergeFrom(account.DeepCopy())
		account.Status.PaymentMethod.ProviderCustomerID = customerID
		if err := r.client.Status().Patch(ctx, &account, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching BillingAccount customer id: %w", err)
		}
	}

	// Patch the PaymentMethodSetup status with the SetupIntent info.
	patch := client.MergeFrom(setup.DeepCopy())
	setup.Status.Phase = billingv1alpha1.PaymentMethodSetupPhaseClientSecretReady
	setup.Status.SetupIntentID = siID
	setup.Status.SetupIntentStatus = siStatus
	setup.Status.ClientSecret = clientSecret
	setup.Status.PublishableKey = cfg.PublishableKey
	setup.Status.ProviderName = cfg.ProviderName
	setup.Status.ObservedGeneration = setup.Generation
	apimeta.SetStatusCondition(&setup.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.PaymentMethodSetupConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: setup.Generation,
		Reason:             "ClientSecretReady",
		Message:            "SetupIntent created; clientSecret available for Stripe Elements.",
	})
	if err := r.client.Status().Patch(ctx, &setup, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching PaymentMethodSetup status: %w", err)
	}

	logger.Info("created Stripe SetupIntent",
		"setupIntent", siID,
		"billingAccount", account.Name,
		"provider", cfg.ProviderName,
	)
	return ctrl.Result{}, nil
}

func (r *PaymentMethodSetupReconciler) markFailed(ctx context.Context, setup *billingv1alpha1.PaymentMethodSetup, reason, msg string) error {
	patch := client.MergeFrom(setup.DeepCopy())
	setup.Status.Phase = billingv1alpha1.PaymentMethodSetupPhaseFailed
	setup.Status.FailureReason = reason
	setup.Status.FailureMessage = msg
	setup.Status.ObservedGeneration = setup.Generation
	apimeta.SetStatusCondition(&setup.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.PaymentMethodSetupConditionSucceeded,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: setup.Generation,
		Reason:             reason,
		Message:            msg,
	})
	return r.client.Status().Patch(ctx, setup, patch)
}

// SetupWithManager wires the reconciler into the controller-runtime manager.
func (r *PaymentMethodSetupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("paymentmethodsetup").
		For(&billingv1alpha1.PaymentMethodSetup{}).
		Complete(r)
}
