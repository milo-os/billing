// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestBillingArrangementApplicationDefaultSetsApplicant(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme", UID: "acme-uid"},
	}
	w := &billingArrangementApplicationWebhook{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build(),
	}

	request := func(op admissionv1.Operation) context.Context {
		return admission.NewContextWithRequest(context.Background(), admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: op,
				UserInfo:  authenticationv1.UserInfo{Username: "jo@acme.example", UID: "user-jo"},
			},
		})
	}
	newApp := func() *billingv1alpha1.BillingArrangementApplication {
		return &billingv1alpha1.BillingArrangementApplication{
			ObjectMeta: metav1.ObjectMeta{Name: "apply", Namespace: "org-acme"},
			Spec: billingv1alpha1.BillingArrangementApplicationSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
				// A client can't apply in someone else's name.
				Applicant: &billingv1alpha1.Applicant{Username: "someone-else@acme.example"},
			},
		}
	}

	app := newApp()
	if err := w.Default(request(admissionv1.Create), app); err != nil {
		t.Fatal(err)
	}
	if got := app.Spec.Applicant; got == nil || got.Username != "jo@acme.example" || got.UID != "user-jo" {
		t.Errorf("applicant = %+v, want the authenticated user", got)
	}
	if len(app.OwnerReferences) != 1 || app.OwnerReferences[0].UID != "acme-uid" || app.OwnerReferences[0].Kind != "BillingAccount" {
		t.Errorf("ownerReferences = %+v, want the billing account", app.OwnerReferences)
	}

	// Updates go through validation, which keeps the applicant fixed;
	// defaulting must not rewrite it to whoever withdraws.
	app = newApp()
	if err := w.Default(request(admissionv1.Update), app); err != nil {
		t.Fatal(err)
	}
	if app.Spec.Applicant.Username != "someone-else@acme.example" || len(app.OwnerReferences) != 0 {
		t.Errorf("update was defaulted: %+v", app)
	}
}
