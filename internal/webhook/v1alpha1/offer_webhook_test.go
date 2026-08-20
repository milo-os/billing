// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/validation"
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
