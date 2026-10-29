// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
	"go.miloapis.com/billing/pkg/features"
)

func TestCallerCanWriteSnapshot(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := authorizationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const allowedUser = "system:control@billing.miloapis.com"

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(
				ctx context.Context,
				c client.WithWatch,
				obj client.Object,
				opts ...client.CreateOption,
			) error {
				sar, ok := obj.(*authorizationv1.SubjectAccessReview)
				if !ok {
					return c.Create(ctx, obj, opts...)
				}
				if sar.Spec.ResourceAttributes == nil {
					t.Fatal("expected ResourceAttributes on SubjectAccessReview")
				}
				attrs := sar.Spec.ResourceAttributes
				if attrs.Verb != validation.OfferSnapshotWriteVerb {
					t.Fatalf("verb = %q, want %q", attrs.Verb, validation.OfferSnapshotWriteVerb)
				}
				if attrs.Group != billingv1alpha1.GroupVersion.Group {
					t.Fatalf("group = %q, want %q", attrs.Group, billingv1alpha1.GroupVersion.Group)
				}
				if attrs.Resource != "offers" {
					t.Fatalf("resource = %q, want offers", attrs.Resource)
				}
				if attrs.Name != "payg-v1" {
					t.Fatalf("name = %q, want payg-v1", attrs.Name)
				}
				sar.Status = authorizationv1.SubjectAccessReviewStatus{
					Allowed: sar.Spec.User == allowedUser,
				}
				return nil
			},
		}).
		Build()

	h := &offerWebhook{Client: cl}

	allowed, err := h.callerCanWriteSnapshot(context.Background(), authenticationv1.UserInfo{
		Username: allowedUser,
	}, "payg-v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected writeSnapshot to be allowed for operator user")
	}

	denied, err := h.callerCanWriteSnapshot(context.Background(), authenticationv1.UserInfo{
		Username: "staff@example.com",
	}, "payg-v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denied {
		t.Fatal("expected writeSnapshot to be denied for staff user")
	}
}

func TestConvertAuthExtra(t *testing.T) {
	got := convertAuthExtra(map[string]authenticationv1.ExtraValue{
		"cluster": {"milo"},
	})
	if got == nil || len(got["cluster"]) != 1 || string(got["cluster"][0]) != "milo" {
		t.Fatalf("convertAuthExtra() = %#v, want cluster=[milo]", got)
	}
	if convertAuthExtra(nil) != nil {
		t.Fatal("expected nil for empty extra")
	}
}

func TestOfferWebhook_SignupCreditFeatureGate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	sp := &billingv1alpha1.ServicePricing{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-allocated"},
		Spec: billingv1alpha1.ServicePricingSpec{
			ChargeType:  billingv1alpha1.ChargeTypeUsage,
			ServiceRef:  "compute.datumapis.com",
			Currency:    "USD",
			Metric:      "compute.datumapis.com/instance/cpu-allocated",
			PricingUnit: "vcpu",
			Rates:       []billingv1alpha1.PricingRate{{Flat: "0.05"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sp).Build()
	h := &offerWebhook{Client: cl}
	ctx := context.Background()

	newOffer := func(withSignupCredit bool) *billingv1alpha1.Offer {
		offer := &billingv1alpha1.Offer{
			ObjectMeta: metav1.ObjectMeta{
				Name: "payg-v1",
				Annotations: map[string]string{
					billingv1alpha1.DisplayNameAnnotation: "Pay As You Go",
				},
			},
			Spec: billingv1alpha1.OfferSpec{
				ChargeTypes: []billingv1alpha1.ChargeType{billingv1alpha1.ChargeTypeUsage},
				LaunchStage: billingv1alpha1.OfferLaunchStageDraft,
				ServicePricingRefs: []billingv1alpha1.ServicePricingRef{
					{Name: "cpu-allocated"},
				},
			},
		}
		if withSignupCredit {
			offer.Spec.SignupCredit = &billingv1alpha1.OfferSignupCredit{
				Amount:      "50.00",
				Currency:    "USD",
				Description: "Welcome bonus",
			}
		}
		return offer
	}

	t.Run("rejects signupCredit on create and update when CreditGrants feature gate is disabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CreditGrants, false)

		if _, err := h.ValidateCreate(ctx, newOffer(false)); err != nil {
			t.Fatalf("expected offer without signupCredit to succeed when gate is disabled, got: %v", err)
		}

		if _, err := h.ValidateCreate(ctx, newOffer(true)); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid error on create with signupCredit when gate is disabled, got: %v", err)
		}

		if _, err := h.ValidateUpdate(ctx, newOffer(false), newOffer(true)); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid error on update adding signupCredit when gate is disabled, got: %v", err)
		}
	})

	t.Run("allows signupCredit on create and update when CreditGrants feature gate is enabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CreditGrants, true)

		if _, err := h.ValidateCreate(ctx, newOffer(true)); err != nil {
			t.Fatalf("expected offer with signupCredit to succeed when gate is enabled, got: %v", err)
		}

		if _, err := h.ValidateUpdate(ctx, newOffer(false), newOffer(true)); err != nil {
			t.Fatalf("expected offer update with signupCredit to succeed when gate is enabled, got: %v", err)
		}
	})
}
