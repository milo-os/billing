// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func validCreditGrant() *billingv1alpha1.CreditGrant {
	d := metav1.Duration{Duration: 720 * time.Hour}
	return &billingv1alpha1.CreditGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "welcome-acme",
			Namespace: "org-acme",
		},
		Spec: billingv1alpha1.CreditGrantSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Amount:            "500.00",
			Currency:          "USD",
			Reason:            billingv1alpha1.CreditGrantReasonPromotional,
			Description:       "Welcome credit",
			ExpiresAfter:      &d,
			Voided:            false,
		},
	}
}

func creditGrantTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, account)...).Build()
}

func TestValidateCreditGrantCreate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*billingv1alpha1.CreditGrant)
		noAccount bool
		wantErr   string
	}{
		{
			name: "valid grant",
		},
		{
			name: "valid grant without expiry",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.ExpiresAfter = nil
			},
		},
		{
			name: "missing billing account",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.BillingAccountRef.Name = "missing"
			},
			wantErr: `spec.billingAccountRef.name: Not found: "missing"`,
		},
		{
			name: "empty billing account name",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.BillingAccountRef.Name = ""
			},
			wantErr: "spec.billingAccountRef.name: Required value",
		},
		{
			name: "zero amount",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = "0.00"
			},
			wantErr: "amount must be a positive decimal amount",
		},
		{
			name: "zero integer amount",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = "0"
			},
			wantErr: "amount must be a positive decimal amount",
		},
		{
			name: "exponential amount notation",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = "1e3"
			},
			wantErr: "amount must be a positive decimal amount",
		},
		{
			name: "unit notation amount",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = "5k"
			},
			wantErr: "amount must be a positive decimal amount",
		},
		{
			name: "negative amount",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = "-10.00"
			},
			wantErr: "amount must be a positive decimal amount",
		},
		{
			name: "empty amount",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Amount = ""
			},
			wantErr: "amount is required",
		},
		{
			name: "invalid currency",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Currency = "EUR"
			},
			wantErr: "currency must be USD",
		},
		{
			name: "invalid reason",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Reason = "InvalidReason"
			},
			wantErr: "supported values: \"Promotional\", \"Sponsorship\", \"Goodwill\"",
		},
		{
			name: "empty description",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Description = ""
			},
			wantErr: "description is required",
		},
		{
			name: "zero expiresAfter duration",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				d := metav1.Duration{Duration: 0}
				g.Spec.ExpiresAfter = &d
			},
			wantErr: "expiresAfter must be greater than zero",
		},
		{
			name: "created as voided",
			mutate: func(g *billingv1alpha1.CreditGrant) {
				g.Spec.Voided = true
			},
			wantErr: "a new credit grant cannot be created as voided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := creditGrantTestClient(t)
			grant := validCreditGrant()
			if tt.mutate != nil {
				tt.mutate(grant)
			}
			errs := ValidateCreditGrantCreate(context.Background(), c, grant)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("wanted error %q, got none", tt.wantErr)
			}
			for _, err := range errs {
				if strings.Contains(err.Error(), tt.wantErr) {
					return
				}
			}
			t.Fatalf("got %v, wanted match for %q", errs, tt.wantErr)
		})
	}
}

func TestValidateCreditGrantUpdate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(oldG, newG *billingv1alpha1.CreditGrant)
		wantErr string
	}{
		{
			name: "voiding active grant is allowed",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.Voided = true
			},
		},
		{
			name: "unvoiding a voided grant is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				oldG.Spec.Voided = true
				newG.Spec.Voided = false
			},
			wantErr: "a voided credit grant cannot be unvoided",
		},
		{
			name: "changing amount is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.Amount = "600.00"
			},
			wantErr: "amount is immutable",
		},
		{
			name: "changing billingAccountRef is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.BillingAccountRef.Name = "other"
			},
			wantErr: "billingAccountRef is immutable",
		},
		{
			name: "changing currency is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.Currency = "EUR"
			},
			wantErr: "currency is immutable",
		},
		{
			name: "changing reason is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.Reason = billingv1alpha1.CreditGrantReasonGoodwill
			},
			wantErr: "reason is immutable",
		},
		{
			name: "changing description is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				newG.Spec.Description = "New description"
			},
			wantErr: "description is immutable",
		},
		{
			name: "changing expiresAfter is forbidden",
			mutate: func(oldG, newG *billingv1alpha1.CreditGrant) {
				d := metav1.Duration{Duration: 100 * time.Hour}
				newG.Spec.ExpiresAfter = &d
			},
			wantErr: "expiresAfter is immutable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := creditGrantTestClient(t)
			oldG := validCreditGrant()
			newG := validCreditGrant()
			if tt.mutate != nil {
				tt.mutate(oldG, newG)
			}
			errs := ValidateCreditGrantUpdate(context.Background(), c, oldG, newG)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("wanted error %q, got none", tt.wantErr)
			}
			for _, err := range errs {
				if strings.Contains(err.Error(), tt.wantErr) {
					return
				}
			}
			t.Fatalf("got %v, wanted match for %q", errs, tt.wantErr)
		})
	}
}

func TestValidateCreditGrantDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("direct deletion of grant is forbidden", func(t *testing.T) {
		c := creditGrantTestClient(t)
		grant := validCreditGrant()
		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) == 0 {
			t.Fatal("expected error, got none")
		}
		want := "credit grants cannot be deleted; void the grant instead"
		if !strings.Contains(errs[0].Error(), want) {
			t.Fatalf("got %v, wanted match for %q", errs[0], want)
		}
	})

	t.Run("deletion allowed when billing account is being deleted", func(t *testing.T) {
		now := metav1.Now()
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "acme",
				Namespace:         "org-acme",
				DeletionTimestamp: &now,
				Finalizers:        []string{"billing.miloapis.com/test"},
			},
			Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		scheme := runtime.NewScheme()
		_ = billingv1alpha1.AddToScheme(scheme)
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build()

		grant := validCreditGrant()
		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) > 0 {
			t.Fatalf("unexpected error during parent account deletion: %v", errs)
		}
	})

	t.Run("deletion allowed when billing account is already deleted", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = billingv1alpha1.AddToScheme(scheme)
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		grant := validCreditGrant()
		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) > 0 {
			t.Fatalf("unexpected error when parent account is gone: %v", errs)
		}
	})

	t.Run("deletion returns internal error on transient error looking up account", func(t *testing.T) {
		r := &testErrorReader{err: errors.New("connection timeout")}
		grant := validCreditGrant()
		errs := ValidateCreditGrantDelete(ctx, r, grant)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
		}
		if errs[0].Type != field.ErrorTypeInternal {
			t.Fatalf("expected internal error, got type %v (%v)", errs[0].Type, errs[0])
		}
	})

	t.Run("deletion allowed when owner reference UID mismatches existing account (account was recreated)", func(t *testing.T) {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme",
				Namespace: "org-acme",
				UID:       "new-account-uid",
			},
			Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		scheme := runtime.NewScheme()
		_ = billingv1alpha1.AddToScheme(scheme)
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build()

		grant := validCreditGrant()
		grant.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: billingv1alpha1.GroupVersion.String(),
			Kind:       "BillingAccount",
			Name:       "acme",
			UID:        "old-deleted-account-uid",
		}}

		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) > 0 {
			t.Fatalf("unexpected error when owner reference UID mismatches: %v", errs)
		}
	})

	t.Run("direct deletion forbidden when owner reference UID matches healthy account", func(t *testing.T) {
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme",
				Namespace: "org-acme",
				UID:       "current-account-uid",
			},
			Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		scheme := runtime.NewScheme()
		_ = billingv1alpha1.AddToScheme(scheme)
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build()

		grant := validCreditGrant()
		grant.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: billingv1alpha1.GroupVersion.String(),
			Kind:       "BillingAccount",
			Name:       "acme",
			UID:        "current-account-uid",
		}}

		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) == 0 {
			t.Fatal("expected error, got none")
		}
		want := "credit grants cannot be deleted; void the grant instead"
		if !strings.Contains(errs[0].Error(), want) {
			t.Fatalf("got %v, wanted match for %q", errs[0], want)
		}
	})

	t.Run("deletion allowed when enclosing namespace is terminating", func(t *testing.T) {
		now := metav1.Now()
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "org-acme",
				DeletionTimestamp: &now,
				Finalizers:        []string{"kubernetes"},
			},
		}
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme",
				Namespace: "org-acme",
				UID:       "current-account-uid",
			},
			Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		}
		scheme := runtime.NewScheme()
		_ = billingv1alpha1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, account).Build()

		grant := validCreditGrant()
		grant.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: billingv1alpha1.GroupVersion.String(),
			Kind:       "BillingAccount",
			Name:       "acme",
			UID:        "current-account-uid",
		}}

		// Account is healthy, but namespace is terminating -> deletion must be allowed
		errs := ValidateCreditGrantDelete(ctx, c, grant)
		if len(errs) > 0 {
			t.Fatalf("unexpected error when namespace is terminating: %v", errs)
		}
	})
}

type testErrorReader struct {
	err error
}

func (r *testErrorReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return r.err
}

func (r *testErrorReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return r.err
}
