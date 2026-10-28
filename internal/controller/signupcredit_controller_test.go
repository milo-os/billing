// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func setupTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add billing scheme: %v", err)
	}
	return scheme
}

func TestSignupCreditReconciler_EnsureSignupCredit(t *testing.T) {
	scheme := setupTestScheme(t)
	const (
		orgNamespace = "org-test"
		accountName  = "test-acct"
		offerName    = "standard-offer"
	)

	validCard := &billingv1alpha1.PaymentMethod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-card-1",
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.PaymentMethodSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: accountName},
			DisplayName:       "Visa 4242",
		},
		Status: billingv1alpha1.PaymentMethodStatus{
			Phase: billingv1alpha1.PaymentMethodPhaseActive,
			Details: &billingv1alpha1.PaymentMethodDetails{
				Type: billingv1alpha1.PaymentMethodInstrumentTypeCard,
				Card: &billingv1alpha1.PaymentMethodCardDetails{
					Brand:       "visa",
					Last4:       "4242",
					Country:     "US",
					ExpiryMonth: 12,
					ExpiryYear:  2030,
				},
			},
		},
	}

	validOfferWithCredit := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{
			Name: offerName,
		},
		Spec: billingv1alpha1.OfferSpec{
			LaunchStage: billingv1alpha1.OfferLaunchStageGA,
			SignupCredit: &billingv1alpha1.OfferSignupCredit{
				Amount:       "50.00",
				Currency:     "USD",
				ExpiresAfter: &metav1.Duration{Duration: 30 * 24 * time.Hour},
				Description:  "Welcome bonus credit",
			},
		},
	}

	validEntitlement := &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "entitlement-1",
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: accountName},
			OfferRef:          billingv1alpha1.OfferReference{Name: offerName},
		},
	}

	validAccount := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accountName,
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.BillingAccountSpec{
			DefaultPaymentMethodRef: &billingv1alpha1.DefaultPaymentMethodRef{
				Name: "pm-card-1",
			},
		},
	}

	t.Run("creates CreditGrant when eligible with active card and offer credit", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, validCard, validOfferWithCredit, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), validAccount)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err = fc.Get(context.Background(), types.NamespacedName{
			Namespace: orgNamespace,
			Name:      SignupCreditGrantName,
		}, &grant)
		if err != nil {
			t.Fatalf("expected signup CreditGrant to be created, got error: %v", err)
		}

		if grant.Spec.BillingAccountRef.Name != accountName {
			t.Errorf("got billingAccountRef %q, want %q", grant.Spec.BillingAccountRef.Name, accountName)
		}
		if grant.Spec.Amount != "50.00" {
			t.Errorf("got amount %q, want %q", grant.Spec.Amount, "50.00")
		}
		if grant.Spec.Currency != "USD" {
			t.Errorf("got currency %q, want %q", grant.Spec.Currency, "USD")
		}
		if grant.Spec.Reason != billingv1alpha1.CreditGrantReasonPromotional {
			t.Errorf("got reason %q, want %q", grant.Spec.Reason, billingv1alpha1.CreditGrantReasonPromotional)
		}
		if grant.Spec.Description != "Welcome bonus credit" {
			t.Errorf("got description %q, want %q", grant.Spec.Description, "Welcome bonus credit")
		}
		if grant.Labels[billingv1alpha1.CreditGrantDefaultLabel] != "true" {
			t.Errorf("expected label %s=true", billingv1alpha1.CreditGrantDefaultLabel)
		}
	})

	t.Run("skips when signup-credit already exists in organization", func(t *testing.T) {
		existingGrant := &billingv1alpha1.CreditGrant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SignupCreditGrantName,
				Namespace: orgNamespace,
			},
			Spec: billingv1alpha1.CreditGrantSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "other-acct"},
				Amount:            "100.00",
				Currency:          "USD",
				Reason:            billingv1alpha1.CreditGrantReasonPromotional,
				Description:       "Existing grant",
			},
		}

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, validCard, validOfferWithCredit, validEntitlement, existingGrant).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), validAccount)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		if err := fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant); err != nil {
			t.Fatalf("failed to get grant: %v", err)
		}
		// Confirm the existing grant was untouched
		if grant.Spec.BillingAccountRef.Name != "other-acct" {
			t.Errorf("existing grant was overwritten")
		}
	})

	t.Run("skips when account has no default payment method", func(t *testing.T) {
		accountWithoutPM := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      accountName,
				Namespace: orgNamespace,
			},
			Spec: billingv1alpha1.BillingAccountSpec{
				DefaultPaymentMethodRef: nil,
			},
		}

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(accountWithoutPM, validOfferWithCredit, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), accountWithoutPM)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err = fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant)
		if err == nil {
			t.Errorf("expected no grant to be created")
		}
	})

	t.Run("skips when default payment method is not active", func(t *testing.T) {
		pendingCard := validCard.DeepCopy()
		pendingCard.Status.Phase = billingv1alpha1.PaymentMethodPhasePending

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, pendingCard, validOfferWithCredit, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), validAccount)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err = fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant)
		if err == nil {
			t.Errorf("expected no grant to be created")
		}
	})

	t.Run("skips when default payment method is not a card (e.g. usBankAccount)", func(t *testing.T) {
		bankPM := validCard.DeepCopy()
		bankPM.Status.Details = &billingv1alpha1.PaymentMethodDetails{
			Type: billingv1alpha1.PaymentMethodInstrumentTypeUSBankAccount,
		}

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, bankPM, validOfferWithCredit, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), validAccount)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err = fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant)
		if err == nil {
			t.Errorf("expected no grant to be created")
		}
	})

	t.Run("skips when offer has no signupCredit", func(t *testing.T) {
		offerWithoutCredit := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{
				Name: offerName,
			},
			Spec: billingv1alpha1.OfferSpec{
				LaunchStage:  billingv1alpha1.OfferLaunchStageGA,
				SignupCredit: nil,
			},
		}

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, validCard, offerWithoutCredit, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		err := r.EnsureSignupCredit(context.Background(), validAccount)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err = fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant)
		if err == nil {
			t.Errorf("expected no grant to be created")
		}
	})

	t.Run("skips when entitled offer is not GA", func(t *testing.T) {
		draftOffer := validOfferWithCredit.DeepCopy()
		draftOffer.Spec.LaunchStage = billingv1alpha1.OfferLaunchStageDraft

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, validCard, draftOffer, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		if err := r.EnsureSignupCredit(context.Background(), validAccount); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var grant billingv1alpha1.CreditGrant
		err := fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant)
		if err == nil {
			t.Errorf("expected no grant to be created")
		}
	})

	t.Run("create leaves a concurrently created grant untouched", func(t *testing.T) {
		// Simulates a stale cache: hasSignupCredit missed a grant another
		// reconcile already created for a sibling account.
		existingGrant := &billingv1alpha1.CreditGrant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SignupCreditGrantName,
				Namespace: orgNamespace,
			},
			Spec: billingv1alpha1.CreditGrantSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "sibling-acct"},
				Amount:            "50.00",
				Currency:          "USD",
				Reason:            billingv1alpha1.CreditGrantReasonPromotional,
				Description:       "Welcome bonus credit",
			},
		}

		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingGrant).
			Build()

		r := &SignupCreditReconciler{client: fc}
		created, err := r.createSignupCreditGrant(context.Background(), validAccount, validOfferWithCredit.Spec.SignupCredit)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if created {
			t.Errorf("expected created=false when grant already exists")
		}

		var grant billingv1alpha1.CreditGrant
		if err := fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant); err != nil {
			t.Fatalf("failed to get grant: %v", err)
		}
		if grant.Spec.BillingAccountRef.Name != "sibling-acct" {
			t.Errorf("existing grant was overwritten: billingAccountRef %q", grant.Spec.BillingAccountRef.Name)
		}
	})
}

func TestSignupCreditReconciler_Reconcile(t *testing.T) {
	scheme := setupTestScheme(t)
	const (
		orgNamespace = "org-test-reconcile"
		accountName  = "test-acct-reconcile"
		offerName    = "standard-offer-reconcile"
	)

	validCard := &billingv1alpha1.PaymentMethod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-card-rec",
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.PaymentMethodSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: accountName},
			DisplayName:       "Visa 4242",
		},
		Status: billingv1alpha1.PaymentMethodStatus{
			Phase: billingv1alpha1.PaymentMethodPhaseActive,
			Details: &billingv1alpha1.PaymentMethodDetails{
				Type: billingv1alpha1.PaymentMethodInstrumentTypeCard,
				Card: &billingv1alpha1.PaymentMethodCardDetails{
					Brand:       "visa",
					Last4:       "4242",
					Country:     "US",
					ExpiryMonth: 12,
					ExpiryYear:  2030,
				},
			},
		},
	}

	validOffer := &billingv1alpha1.Offer{
		ObjectMeta: metav1.ObjectMeta{
			Name: offerName,
		},
		Spec: billingv1alpha1.OfferSpec{
			LaunchStage: billingv1alpha1.OfferLaunchStageGA,
			SignupCredit: &billingv1alpha1.OfferSignupCredit{
				Amount:      "50.00",
				Currency:    "USD",
				Description: "Reconcile welcome bonus",
			},
		},
	}

	validEntitlement := &billingv1alpha1.BillingEntitlement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "entitlement-rec",
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.BillingEntitlementSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: accountName},
			OfferRef:          billingv1alpha1.OfferReference{Name: offerName},
		},
	}

	validAccount := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accountName,
			Namespace: orgNamespace,
		},
		Spec: billingv1alpha1.BillingAccountSpec{
			DefaultPaymentMethodRef: &billingv1alpha1.DefaultPaymentMethodRef{
				Name: "pm-card-rec",
			},
		},
	}

	t.Run("returns empty result and no error when BillingAccount not found", func(t *testing.T) {
		fc := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &SignupCreditReconciler{client: fc}
		res, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: orgNamespace, Name: "nonexistent"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RequeueAfter != 0 {
			t.Errorf("expected zero result")
		}
	})

	t.Run("returns empty result and no error when BillingAccount is being deleted", func(t *testing.T) {
		now := metav1.Now()
		deletingAccount := validAccount.DeepCopy()
		deletingAccount.Finalizers = []string{"billing.miloapis.com/test"}
		deletingAccount.DeletionTimestamp = &now

		fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingAccount).Build()
		r := &SignupCreditReconciler{client: fc}
		res, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: orgNamespace, Name: accountName},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RequeueAfter != 0 {
			t.Errorf("expected zero result")
		}
	})

	t.Run("successfully reconciles and creates signup-credit", func(t *testing.T) {
		fc := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(validAccount, validCard, validOffer, validEntitlement).
			WithIndex(&billingv1alpha1.BillingEntitlement{}, BillingEntitlementBillingAccountRefField, func(obj client.Object) []string {
				be := obj.(*billingv1alpha1.BillingEntitlement)
				return []string{be.Spec.BillingAccountRef.Name}
			}).
			Build()

		r := &SignupCreditReconciler{client: fc}
		res, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: orgNamespace, Name: accountName},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RequeueAfter != 0 {
			t.Errorf("expected zero result")
		}

		var grant billingv1alpha1.CreditGrant
		if err := fc.Get(context.Background(), types.NamespacedName{Namespace: orgNamespace, Name: SignupCreditGrantName}, &grant); err != nil {
			t.Fatalf("expected grant to exist, got error: %v", err)
		}
		if grant.Spec.Amount != "50.00" {
			t.Errorf("got amount %q, want 50.00", grant.Spec.Amount)
		}
	})
}
