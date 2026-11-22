// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestChargeWebhook_Default(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	pricing := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-connect-install-v1", Namespace: "milo-system"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType: billingv1alpha1.ChargeTypeOneTime, DisplayName: "Cross-connect install", Amount: "300",
		},
	}
	w := &chargeWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(account, pricing).Build(),
		now:    func() time.Time { return time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC) },
	}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			UserInfo:  authenticationv1.UserInfo{Username: "sam@example.com"},
		},
	})

	charge := &billingv1alpha1.Charge{
		ObjectMeta: metav1.ObjectMeta{Name: "install", Namespace: "org-acme"},
		Spec: billingv1alpha1.ChargeSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: pricing.Name},
			Quantity:          "2",
		},
	}
	if err := w.Default(ctx, charge); err != nil {
		t.Fatal(err)
	}
	if charge.Spec.Description != "Cross-connect install" || charge.Spec.UnitAmount != "300.00" {
		t.Errorf("catalog defaults not applied: %+v", charge.Spec)
	}
	if p := charge.Spec.ServicePeriod; p == nil || p.Start != "2026-11-01" || p.End != "2026-11-30" {
		t.Errorf("period not defaulted: %+v", p)
	}
	if got := charge.Annotations[billingv1alpha1.ChargeAddedByAnnotation]; got != "sam@example.com" {
		t.Errorf("added-by = %q", got)
	}
	if refs := charge.OwnerReferences; len(refs) != 1 || refs[0].UID != "acme-uid" {
		t.Errorf("owner references = %+v", refs)
	}

	// Defaulting only runs on create.
	update := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update},
	})
	untouched := &billingv1alpha1.Charge{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "org-acme"}}
	if err := w.Default(update, untouched); err != nil {
		t.Fatal(err)
	}
	if untouched.Spec.Currency != "" || untouched.Annotations != nil {
		t.Errorf("update was defaulted: %+v", untouched)
	}
}
