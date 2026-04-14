// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = billingv1alpha1.AddToScheme(s)
	return s
}

// Tests for validateBillingAccountReady (tested directly to avoid
// MatchingFields limitation of the fake client).
func TestValidateBillingAccountReady(t *testing.T) {
	tests := []struct {
		name    string
		account *billingv1alpha1.BillingAccount
		wantErr bool
	}{
		{
			name: "account is Ready",
			account: &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-account",
					Namespace: "default",
				},
				Status: billingv1alpha1.BillingAccountStatus{
					Phase: billingv1alpha1.BillingAccountPhaseReady,
				},
			},
			wantErr: false,
		},
		{
			name: "account is Suspended",
			account: &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-account",
					Namespace: "default",
				},
				Status: billingv1alpha1.BillingAccountStatus{
					Phase: billingv1alpha1.BillingAccountPhaseSuspended,
				},
			},
			wantErr: true,
		},
		{
			name:    "account does not exist",
			account: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme()
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.account != nil {
				builder = builder.WithObjects(tt.account).WithStatusSubresource(tt.account)
			}
			cl := builder.Build()

			binding := &billingv1alpha1.BillingAccountBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-binding",
					Namespace: "default",
				},
				Spec: billingv1alpha1.BillingAccountBindingSpec{
					BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "test-account"},
					ProjectRef:        billingv1alpha1.ProjectRef{Name: "test-project"},
				},
			}

			opts := BillingAccountBindingValidationOptions{
				Context: context.Background(),
				Client:  cl,
			}

			errs := validateBillingAccountReady(binding, opts)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateBillingAccountReady() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateBillingAccountReady_AccountBeingDeleted(t *testing.T) {
	now := metav1.Now()
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-account",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		},
		Status: billingv1alpha1.BillingAccountStatus{
			Phase: billingv1alpha1.BillingAccountPhaseReady,
		},
	}

	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(account).WithStatusSubresource(account).Build()

	binding := &billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-binding",
			Namespace: "default",
		},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "deleting-account"},
			ProjectRef:        billingv1alpha1.ProjectRef{Name: "test-project"},
		},
	}

	opts := BillingAccountBindingValidationOptions{
		Context: context.Background(),
		Client:  cl,
	}

	errs := validateBillingAccountReady(binding, opts)
	if len(errs) == 0 {
		t.Error("expected error when account is being deleted, got none")
	}
}

func TestValidateBillingAccountReady_CrossNamespaceRejected(t *testing.T) {
	// Account in a different namespace -- Get uses the binding's namespace,
	// so the account won't be found.
	scheme := newScheme()
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-ns-account",
			Namespace: "other-namespace",
		},
		Status: billingv1alpha1.BillingAccountStatus{
			Phase: billingv1alpha1.BillingAccountPhaseReady,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(account).WithStatusSubresource(account).Build()

	binding := &billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cross-ns-binding",
			Namespace: "default",
		},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "other-ns-account"},
			ProjectRef:        billingv1alpha1.ProjectRef{Name: "test-project"},
		},
	}

	opts := BillingAccountBindingValidationOptions{
		Context: context.Background(),
		Client:  cl,
	}

	errs := validateBillingAccountReady(binding, opts)
	if len(errs) == 0 {
		t.Error("expected error for cross-namespace binding, got none")
	}

	// Should be a NotFound error since Get uses the binding's namespace
	found := false
	for _, e := range errs {
		if e.Type == "FieldValueNotFound" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected NotFound error, got: %v", errs)
	}
}

// Note: validateSingleActiveBinding uses MatchingFields which requires
// field indexers not available in the fake client. The single-binding rule
// is tested via envtest in the controller integration tests
// (billingaccountbinding_controller_test.go).
