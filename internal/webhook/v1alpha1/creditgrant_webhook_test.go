// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestCreditGrantWebhook_Default(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
	}
	w := &creditGrantWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build(),
	}

	request := func(op admissionv1.Operation) context.Context {
		return admission.NewContextWithRequest(context.Background(), admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: op,
			},
		})
	}

	newGrant := func() *billingv1alpha1.CreditGrant {
		d := metav1.Duration{Duration: 720 * time.Hour}
		return &billingv1alpha1.CreditGrant{
			ObjectMeta: metav1.ObjectMeta{Name: "welcome", Namespace: "org-acme"},
			Spec: billingv1alpha1.CreditGrantSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				Amount:            "50.00",
				Currency:          "USD",
				Reason:            billingv1alpha1.CreditGrantReasonPromotional,
				Description:       "Welcome",
				ExpiresAfter:      &d,
			},
		}
	}

	// On create, ownerReference to BillingAccount should be added
	grant := newGrant()
	if err := w.Default(request(admissionv1.Create), grant); err != nil {
		t.Fatal(err)
	}
	if len(grant.OwnerReferences) != 1 || grant.OwnerReferences[0].UID != "acme-uid" || grant.OwnerReferences[0].Kind != "BillingAccount" {
		t.Errorf("ownerReferences = %+v, want the billing account", grant.OwnerReferences)
	}

	// On create when billing account does not exist, no ownerReference should be added (left to validation)
	grant = newGrant()
	grant.Spec.BillingAccountRef.Name = "nonexistent"
	if err := w.Default(request(admissionv1.Create), grant); err != nil {
		t.Fatal(err)
	}
	if len(grant.OwnerReferences) != 0 {
		t.Errorf("expected no ownerReferences for nonexistent account, got %+v", grant.OwnerReferences)
	}

	// On create when billing account name is empty, no ownerReference should be added
	grant = newGrant()
	grant.Spec.BillingAccountRef.Name = ""
	if err := w.Default(request(admissionv1.Create), grant); err != nil {
		t.Fatal(err)
	}
	if len(grant.OwnerReferences) != 0 {
		t.Errorf("expected no ownerReferences for empty account name, got %+v", grant.OwnerReferences)
	}

	// On update, ownerReference should not be changed or added
	grant = newGrant()
	if err := w.Default(request(admissionv1.Update), grant); err != nil {
		t.Fatal(err)
	}
	if len(grant.OwnerReferences) != 0 {
		t.Errorf("update was defaulted: %+v", grant)
	}
}

func TestCreditGrantWebhook_Validation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	w := &creditGrantWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build(),
	}

	ctx := context.Background()
	grant := &billingv1alpha1.CreditGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "welcome", Namespace: "org-acme"},
		Spec: billingv1alpha1.CreditGrantSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Amount:            "50.00",
			Currency:          "USD",
			Reason:            billingv1alpha1.CreditGrantReasonPromotional,
			Description:       "Welcome",
		},
	}

	// ValidateCreate success
	if _, err := w.ValidateCreate(ctx, grant); err != nil {
		t.Fatalf("unexpected error on valid create: %v", err)
	}

	// ValidateCreate failure (missing account)
	invalidGrant := grant.DeepCopy()
	invalidGrant.Spec.BillingAccountRef.Name = "missing"
	if _, err := w.ValidateCreate(ctx, invalidGrant); err == nil {
		t.Fatal("expected error on missing account, got nil")
	}

	// ValidateUpdate success (voiding)
	updatedGrant := grant.DeepCopy()
	updatedGrant.Spec.Voided = true
	if _, err := w.ValidateUpdate(ctx, grant, updatedGrant); err != nil {
		t.Fatalf("unexpected error on valid update (voiding): %v", err)
	}

	// ValidateUpdate failure (changing amount)
	invalidUpdate := grant.DeepCopy()
	invalidUpdate.Spec.Amount = "100.00"
	if _, err := w.ValidateUpdate(ctx, grant, invalidUpdate); err == nil {
		t.Fatal("expected error on changing amount, got nil")
	}

	// ValidateDelete failure (direct delete on active account is forbidden)
	if _, err := w.ValidateDelete(ctx, grant); err == nil {
		t.Fatal("expected error on delete credit grant, got nil")
	}

	// ValidateDelete success (when parent billing account is being deleted)
	now := metav1.Now()
	accountDeleting := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "acme-del",
			Namespace:         "org-acme",
			UID:               "acme-del-uid",
			DeletionTimestamp: &now,
			Finalizers:        []string{"billing.miloapis.com/test"},
		},
	}
	wDeleting := &creditGrantWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(accountDeleting).Build(),
	}
	grantOnDeletingAccount := grant.DeepCopy()
	grantOnDeletingAccount.Spec.BillingAccountRef.Name = "acme-del"
	if _, err := wDeleting.ValidateDelete(ctx, grantOnDeletingAccount); err != nil {
		t.Fatalf("unexpected error on delete during account deletion: %v", err)
	}

	// ValidateDelete success (when parent billing account is already deleted/gone)
	wGone := &creditGrantWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}
	grantOnGoneAccount := grant.DeepCopy()
	grantOnGoneAccount.Spec.BillingAccountRef.Name = "acme-gone"
	if _, err := wGone.ValidateDelete(ctx, grantOnGoneAccount); err != nil {
		t.Fatalf("unexpected error on delete when account is gone: %v", err)
	}

	// ValidateDelete failure (transient reader error)
	wTransient := &creditGrantWebhook{
		reader: &testErrorReader{err: errors.New("transient network error")},
	}
	if _, err := wTransient.ValidateDelete(ctx, grant); err == nil {
		t.Fatal("expected error on delete with transient reader error, got nil")
	}
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
