// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"errors"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
)

// TestBillingAccountDefaultPaymentMethodReadsAPIServer covers a card being
// designated default moments after it turned Active: the cache still holds
// the AwaitingConfirmation copy while the API server already has Active.
func TestBillingAccountDefaultPaymentMethodReadsAPIServer(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authorizationv1.AddToScheme(scheme); err != nil {
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
		WithObjects(card(billingv1alpha1.PaymentMethodPhaseAwaitingConfirmation)).
		WithInterceptorFuncs(interceptor.Funcs{Create: allowNothing}).Build()
	apiServer := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(card(billingv1alpha1.PaymentMethodPhaseActive)).Build()

	webhook := &billingAccountWebhook{client: cache, reader: apiServer}

	oldAccount := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	newAccount := oldAccount.DeepCopy()
	newAccount.Spec.DefaultPaymentMethodRef = &billingv1alpha1.DefaultPaymentMethodRef{Name: "first-card"}

	// A customer: the fake client's SubjectAccessReviews allow nothing.
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{UserInfo: authenticationv1.UserInfo{Username: "alice@example.com"}},
	})
	if _, err := webhook.ValidateUpdate(ctx, oldAccount, newAccount); err != nil {
		t.Fatalf("ValidateUpdate() = %v, want card accepted as default", err)
	}
	if _, err := webhook.ValidateCreate(context.Background(), newAccount); err != nil {
		t.Fatalf("ValidateCreate() = %v, want card accepted as default", err)
	}
}

// allowNothing answers every SubjectAccessReview with not allowed.
func allowNothing(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*authorizationv1.SubjectAccessReview); ok {
		return nil
	}
	return c.Create(ctx, obj, opts...)
}

// TestBillingAccountContactDetailsEditorLimits covers staff's Billing Account
// Editor: contact details and tax IDs only, unless the caller can also manage
// the account's settings.
func TestBillingAccountContactDetailsEditorLimits(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authorizationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Each caller's verbs on billingaccounts/acme in organization-acme.
	grants := map[string]map[string]bool{
		"staff@example.com": {validation.BillingAccountEditContactDetailsVerb: true},
		"staff-owner@example.com": {
			validation.BillingAccountEditContactDetailsVerb: true,
			validation.BillingAccountManageSettingsVerb:     true,
		},
		"alice@example.com": {validation.BillingAccountManageSettingsVerb: true},
	}
	reviews := 0
	cl := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			sar, ok := obj.(*authorizationv1.SubjectAccessReview)
			if !ok {
				return c.Create(ctx, obj, opts...)
			}
			reviews++
			attrs := sar.Spec.ResourceAttributes
			if attrs.Group != billingv1alpha1.GroupVersion.Group || attrs.Resource != "billingaccounts" ||
				attrs.Namespace != "organization-acme" || attrs.Name != "acme" {
				t.Fatalf("unexpected review of %+v", attrs)
			}
			if got := sar.Spec.Extra["iam.miloapis.com/parent-name"]; len(got) != 1 || got[0] != "acme" {
				t.Fatalf("extras not passed on: %v", sar.Spec.Extra)
			}
			if sar.Spec.User == "broken@example.com" {
				return errors.New("authorizer unavailable")
			}
			sar.Status.Allowed = grants[sar.Spec.User][attrs.Verb]
			return nil
		},
	}).Build()
	webhook := &billingAccountWebhook{client: cl, reader: cl}

	base := func() *billingv1alpha1.BillingAccount {
		return &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "organization-acme"},
			Spec: billingv1alpha1.BillingAccountSpec{
				CurrencyCode: "USD",
				ContactInfo:  &billingv1alpha1.BillingContactInfo{Email: "billing@acme.example"},
			},
		}
	}
	editContact := func(a *billingv1alpha1.BillingAccount) {
		a.Spec.ContactInfo.Email = "ap@acme.example"
		a.Spec.TaxIDs = []billingv1alpha1.TaxID{{Type: "gb_vat", Value: "GB123456789"}}
	}
	setDefaultForNewProjects := func(a *billingv1alpha1.BillingAccount) {
		a.Spec.DefaultForNewProjects = true
		a.Annotations = map[string]string{"kubernetes.io/display-name": "Acme"}
	}

	tests := []struct {
		name        string
		user        string
		change      func(*billingv1alpha1.BillingAccount)
		wantErr     string
		wantReviews int
	}{
		{name: "staff edit the contact details", user: "staff@example.com", change: editContact},
		{
			name:        "staff can't change the account's settings",
			user:        "staff@example.com",
			change:      setDefaultForNewProjects,
			wantErr:     "can change only a billing account's contact details",
			wantReviews: 2,
		},
		{
			name:        "staff who also manage the account aren't limited",
			user:        "staff-owner@example.com",
			change:      setDefaultForNewProjects,
			wantReviews: 2,
		},
		{
			name:        "a customer changes the settings",
			user:        "alice@example.com",
			change:      setDefaultForNewProjects,
			wantReviews: 1,
		},
		{
			name:        "a controller, holding neither verb, isn't limited",
			user:        "system:control@billing.miloapis.com",
			change:      setDefaultForNewProjects,
			wantReviews: 1,
		},
		{
			name:        "the limit fails closed when it can't be checked",
			user:        "broken@example.com",
			change:      setDefaultForNewProjects,
			wantErr:     "authorizer unavailable",
			wantReviews: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reviews = 0
			oldAccount := base()
			newAccount := base()
			tt.change(newAccount)
			ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{UserInfo: authenticationv1.UserInfo{
					Username: tt.user,
					Extra:    map[string]authenticationv1.ExtraValue{"iam.miloapis.com/parent-name": {"acme"}},
				}},
			})
			_, err := webhook.ValidateUpdate(ctx, oldAccount, newAccount)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("ValidateUpdate() = %v, want allowed", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("ValidateUpdate() = %v, want an error containing %q", err, tt.wantErr)
			}
			if reviews != tt.wantReviews {
				t.Fatalf("access reviews = %d, want %d", reviews, tt.wantReviews)
			}
		})
	}

	t.Run("without the admission request", func(t *testing.T) {
		newAccount := base()
		setDefaultForNewProjects(newAccount)
		if _, err := webhook.ValidateUpdate(context.Background(), base(), newAccount); err == nil {
			t.Fatal("ValidateUpdate() allowed a settings change without knowing the caller")
		}
	})
}
