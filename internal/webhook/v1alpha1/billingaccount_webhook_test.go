// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// TestBillingAccountDefaultPaymentMethodReadsAPIServer covers a card being
// designated default moments after it turned Active: the cache still holds
// the AwaitingConfirmation copy while the API server already has Active.
func TestBillingAccountDefaultPaymentMethodReadsAPIServer(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	card := func(phase billingv1alpha1.PaymentMethodPhase) *billingv1alpha1.PaymentMethod {
		return &billingv1alpha1.PaymentMethod{
			ObjectMeta: metav1.ObjectMeta{Name: "first-card", Namespace: "org-acme"},
			Spec: billingv1alpha1.PaymentMethodSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			},
			Status: billingv1alpha1.PaymentMethodStatus{Phase: phase},
		}
	}
	cache := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(card(billingv1alpha1.PaymentMethodPhaseAwaitingConfirmation)).Build()
	apiServer := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(card(billingv1alpha1.PaymentMethodPhaseActive)).Build()

	webhook := &billingAccountWebhook{client: cache, reader: apiServer}

	oldAccount := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	newAccount := oldAccount.DeepCopy()
	newAccount.Spec.DefaultPaymentMethodRef = &billingv1alpha1.DefaultPaymentMethodRef{Name: "first-card"}

	if _, err := webhook.ValidateUpdate(context.Background(), oldAccount, newAccount); err != nil {
		t.Fatalf("ValidateUpdate() = %v, want card accepted as default", err)
	}
	if _, err := webhook.ValidateCreate(context.Background(), newAccount); err != nil {
		t.Fatalf("ValidateCreate() = %v, want card accepted as default", err)
	}
}
