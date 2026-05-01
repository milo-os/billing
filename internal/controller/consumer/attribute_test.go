// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = billingv1alpha1.AddToScheme(s)
	return s
}

func newTestBindingCache(bindings ...billingv1alpha1.BillingAccountBinding) *BillingAccountBindingCache {
	bc := &BillingAccountBindingCache{activeByProject: make(map[string]*billingv1alpha1.BillingAccountBinding)}
	for i := range bindings {
		bc.upsert(&bindings[i])
	}
	return bc
}

func fakeClientWithAccounts(accounts ...billingv1alpha1.BillingAccount) *fake.ClientBuilder {
	objs := make([]runtime.Object, len(accounts))
	for i := range accounts {
		objs[i] = &accounts[i]
	}
	_ = objs
	b := fake.NewClientBuilder().WithScheme(newTestScheme())
	for i := range accounts {
		b = b.WithObjects(&accounts[i])
	}
	return b
}

func activeBinding(name, project, account string) billingv1alpha1.BillingAccountBinding {
	return billingv1alpha1.BillingAccountBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: billingv1alpha1.BillingAccountBindingSpec{
			ProjectRef:        billingv1alpha1.ProjectRef{Name: project},
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account},
		},
		Status: billingv1alpha1.BillingAccountBindingStatus{
			Phase: billingv1alpha1.BillingAccountBindingPhaseActive,
		},
	}
}

func readyAccount(name string) billingv1alpha1.BillingAccount {
	return billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		Status:     billingv1alpha1.BillingAccountStatus{Phase: billingv1alpha1.BillingAccountPhaseReady},
	}
}

func TestAttribute_ActiveBinding_Matches(t *testing.T) {
	binding := activeBinding("binding-1", "my-project", "acct-1")
	account := readyAccount("acct-1")

	bc := newTestBindingCache(binding)
	c := fakeClientWithAccounts(account).Build()

	result, err := attribute(context.Background(), "my-project", bc, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Errorf("expected attribution OK, got reason=%s", result.Reason)
	}
	if result.BillingAccountRef != "acct-1" {
		t.Errorf("expected BillingAccountRef=acct-1, got %s", result.BillingAccountRef)
	}
}

func TestAttribute_NoActiveBinding_Fails(t *testing.T) {
	bc := newTestBindingCache() // empty

	result, err := attribute(context.Background(), "my-project", bc, fake.NewClientBuilder().WithScheme(newTestScheme()).Build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected attribution to fail when no active binding exists")
	}
	if result.Reason != ReasonAttributionFailure {
		t.Errorf("expected ReasonAttributionFailure, got %s", result.Reason)
	}
}

func TestAttribute_SupersededBinding_NotUsed(t *testing.T) {
	binding := activeBinding("binding-old", "my-project", "acct-old")
	binding.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded

	bc := newTestBindingCache(binding) // superseded bindings are not indexed

	result, err := attribute(context.Background(), "my-project", bc, fake.NewClientBuilder().WithScheme(newTestScheme()).Build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected attribution to fail when only a superseded binding exists")
	}
}

func TestAttribute_NonReadyAccount_Fails(t *testing.T) {
	binding := activeBinding("binding-1", "my-project", "acct-suspended")
	account := billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acct-suspended", Namespace: "default"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		Status:     billingv1alpha1.BillingAccountStatus{Phase: billingv1alpha1.BillingAccountPhaseSuspended},
	}

	bc := newTestBindingCache(binding)
	c := fakeClientWithAccounts(account).Build()

	result, err := attribute(context.Background(), "my-project", bc, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected attribution to fail for non-Ready billing account")
	}
	if result.Reason != ReasonAttributionFailure {
		t.Errorf("expected ReasonAttributionFailure, got %s", result.Reason)
	}
}

func TestAttribute_BindingCacheEvictsOnPhaseTransition(t *testing.T) {
	binding := activeBinding("binding-1", "proj", "acct-1")
	bc := newTestBindingCache(binding)

	// Transition to Superseded — cache should evict it.
	binding.Status.Phase = billingv1alpha1.BillingAccountBindingPhaseSuperseded
	bc.upsert(&binding)

	result, err := attribute(context.Background(), "proj", bc, fake.NewClientBuilder().WithScheme(newTestScheme()).Build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected attribution to fail after binding transitioned to Superseded")
	}
}

// Ensure time is imported (used in test helpers retained from prior version).
var _ = time.Now
