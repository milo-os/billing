// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// SignupCreditGrantName is the fixed deterministic name of the signup
	// credit grant. Because Kubernetes guarantees name uniqueness per
	// namespace, this ensures at most one signup credit exists per organization.
	SignupCreditGrantName = "signup-credit"
)

// SignupCreditReconciler reconciles BillingAccounts to evaluate eligibility
// and grant automated welcome credits based on the account's entitled Offer.
type SignupCreditReconciler struct {
	client client.Client
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=paymentmethods,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingentitlements,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=offers,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=creditgrants,verbs=get;list;watch;create

func (r *SignupCreditReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var account billingv1alpha1.BillingAccount
	if err := r.client.Get(ctx, req.NamespacedName, &account); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !account.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if err := r.EnsureSignupCredit(ctx, &account); err != nil {
		logger.Error(err, "failed to ensure signup credit",
			"billingAccount", account.Name,
			"namespace", account.Namespace,
		)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// EnsureSignupCredit evaluates whether the organization already has a signup
// credit and, if eligible, grants one to the account based on its entitled Offer.
func (r *SignupCreditReconciler) EnsureSignupCredit(
	ctx context.Context,
	account *billingv1alpha1.BillingAccount,
) error {
	logger := log.FromContext(ctx)

	// 1. One signup credit per organization namespace.
	alreadyGranted, err := r.hasSignupCredit(ctx, account.Namespace)
	if err != nil {
		return err
	}
	if alreadyGranted {
		return nil
	}

	// 2. Default payment method must be a confirmed, active card.
	isReadyCard, err := r.hasReadyDefaultPaymentCard(ctx, account)
	if err != nil {
		return err
	}
	if !isReadyCard {
		return nil
	}

	// 3. Resolve the account's active BillingEntitlement and Offer signupCredit specification.
	signupCredit, err := r.getEntitledSignupCredit(ctx, account)
	if err != nil {
		return err
	}
	if signupCredit == nil {
		return nil
	}

	// 4. Create the CreditGrant.
	created, err := r.createSignupCreditGrant(ctx, account, signupCredit)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}

	logger.Info("created signup CreditGrant",
		"creditGrant", SignupCreditGrantName,
		"billingAccount", account.Name,
		"namespace", account.Namespace,
		"amount", signupCredit.Amount,
	)
	return nil
}

// hasSignupCredit checks if a CreditGrant named signup-credit already exists in the namespace.
func (r *SignupCreditReconciler) hasSignupCredit(ctx context.Context, namespace string) (bool, error) {
	var existing billingv1alpha1.CreditGrant
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      SignupCreditGrantName,
	}
	if err := r.client.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking existing signup credit grant: %w", err)
	}
	return true, nil
}

// hasReadyDefaultPaymentCard verifies that the billing account has a default payment method
// referencing an Active card instrument in the same namespace.
func (r *SignupCreditReconciler) hasReadyDefaultPaymentCard(
	ctx context.Context,
	account *billingv1alpha1.BillingAccount,
) (bool, error) {
	if account.Spec.DefaultPaymentMethodRef == nil || account.Spec.DefaultPaymentMethodRef.Name == "" {
		return false, nil
	}

	var pm billingv1alpha1.PaymentMethod
	key := types.NamespacedName{
		Namespace: account.Namespace,
		Name:      account.Spec.DefaultPaymentMethodRef.Name,
	}
	if err := r.client.Get(ctx, key, &pm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting default payment method %q: %w", key.Name, err)
	}

	if !pm.DeletionTimestamp.IsZero() {
		return false, nil
	}

	if pm.Status.Phase != billingv1alpha1.PaymentMethodPhaseActive {
		return false, nil
	}

	if pm.Status.Details == nil || pm.Status.Details.Type != billingv1alpha1.PaymentMethodInstrumentTypeCard {
		return false, nil
	}

	return true, nil
}

// getEntitledSignupCredit looks up the BillingEntitlement for the billing account
// and returns the signupCredit configured on its referenced Offer, or nil if none.
func (r *SignupCreditReconciler) getEntitledSignupCredit(
	ctx context.Context,
	account *billingv1alpha1.BillingAccount,
) (*billingv1alpha1.OfferSignupCredit, error) {
	var entitlementList billingv1alpha1.BillingEntitlementList
	if err := r.client.List(ctx, &entitlementList,
		client.InNamespace(account.Namespace),
		client.MatchingFields{BillingEntitlementBillingAccountRefField: account.Name},
	); err != nil {
		return nil, fmt.Errorf("listing billing entitlements for account %q: %w", account.Name, err)
	}

	if len(entitlementList.Items) == 0 {
		return nil, nil
	}

	be := &entitlementList.Items[0]
	if be.Spec.OfferRef.Name == "" {
		return nil, nil
	}

	var offer billingv1alpha1.Offer
	if err := r.client.Get(ctx, types.NamespacedName{Name: be.Spec.OfferRef.Name}, &offer); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting offer %q: %w", be.Spec.OfferRef.Name, err)
	}

	// Entitlements may only reference GA Offers; admission checks this at
	// create time, so re-check here for defense in depth.
	if offer.Spec.LaunchStage != billingv1alpha1.OfferLaunchStageGA {
		return nil, nil
	}

	return offer.Spec.SignupCredit, nil
}

// createSignupCreditGrant creates the promotional CreditGrant for the billing
// account. It uses Create rather than Apply so the API server's name uniqueness
// is the atomic guard: if another reconcile already created the grant (e.g.
// for a sibling account while the cache was stale), the existing grant is left
// untouched and created is false.
func (r *SignupCreditReconciler) createSignupCreditGrant(
	ctx context.Context,
	account *billingv1alpha1.BillingAccount,
	credit *billingv1alpha1.OfferSignupCredit,
) (created bool, err error) {
	currency := credit.Currency
	if currency == "" {
		currency = "USD"
	}

	var expiresAfter *metav1.Duration
	if credit.ExpiresAfter != nil && credit.ExpiresAfter.Duration > 0 {
		expiresAfter = &metav1.Duration{Duration: credit.ExpiresAfter.Duration}
	}

	grant := &billingv1alpha1.CreditGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SignupCreditGrantName,
			Namespace: account.Namespace,
			Labels: map[string]string{
				billingv1alpha1.CreditGrantDefaultLabel: "true",
			},
		},
		Spec: billingv1alpha1.CreditGrantSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{
				Name: account.Name,
			},
			Amount:       credit.Amount,
			Currency:     currency,
			Reason:       billingv1alpha1.CreditGrantReasonPromotional,
			Description:  credit.Description,
			ExpiresAfter: expiresAfter,
		},
	}

	if err := r.client.Create(ctx, grant); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, fmt.Errorf("creating CreditGrant %q: %w", SignupCreditGrantName, err)
	}

	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SignupCreditReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named("signupcredit").
		For(&billingv1alpha1.BillingAccount{}).
		Watches(
			&billingv1alpha1.PaymentMethod{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					pm, ok := obj.(*billingv1alpha1.PaymentMethod)
					if !ok || pm.Spec.BillingAccountRef.Name == "" {
						return nil
					}
					return []reconcile.Request{{
						NamespacedName: client.ObjectKey{
							Name:      pm.Spec.BillingAccountRef.Name,
							Namespace: pm.Namespace,
						},
					}}
				},
			),
		).
		Watches(
			&billingv1alpha1.BillingEntitlement{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					be, ok := obj.(*billingv1alpha1.BillingEntitlement)
					if !ok || be.Spec.BillingAccountRef.Name == "" {
						return nil
					}
					return []reconcile.Request{{
						NamespacedName: client.ObjectKey{
							Name:      be.Spec.BillingAccountRef.Name,
							Namespace: be.Namespace,
						},
					}}
				},
			),
		).
		Complete(r)
}
