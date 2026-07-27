// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func TestValidateInvoicePeriod(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	end := metav1.NewTime(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	beforeStart := metav1.NewTime(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name    string
		period  billingv1alpha1.InvoicePeriod
		wantErr bool
	}{
		{
			name:   "valid equal bounds",
			period: billingv1alpha1.InvoicePeriod{Start: start, End: start},
		},
		{
			name:   "valid ordered window",
			period: billingv1alpha1.InvoicePeriod{Start: start, End: end},
		},
		{
			name:    "start after end",
			period:  billingv1alpha1.InvoicePeriod{Start: start, End: beforeStart},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateInvoicePeriod(tt.period, field.NewPath("spec", "period"))
			if tt.wantErr && len(errs) == 0 {
				t.Fatal("expected error, got none")
			}
			if !tt.wantErr && len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateInvoiceOwnerReference(t *testing.T) {
	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acme",
			UID:  types.UID("account-uid"),
		},
	}

	tests := []struct {
		name    string
		invoice *billingv1alpha1.Invoice
		wantErr bool
	}{
		{
			name: "valid non-controller ownerReference",
			invoice: &billingv1alpha1.Invoice{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: billingv1alpha1.GroupVersion.String(),
						Kind:       "BillingAccount",
						Name:       "acme",
						UID:        "account-uid",
						Controller: ptr.To(false),
					}},
				},
			},
		},
		{
			name: "valid when controller is unset",
			invoice: &billingv1alpha1.Invoice{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: billingv1alpha1.GroupVersion.String(),
						Kind:       "BillingAccount",
						Name:       "acme",
						UID:        "account-uid",
					}},
				},
			},
		},
		{
			name:    "missing ownerReference",
			invoice: &billingv1alpha1.Invoice{},
			wantErr: true,
		},
		{
			name: "controller true rejected",
			invoice: &billingv1alpha1.Invoice{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: billingv1alpha1.GroupVersion.String(),
						Kind:       "BillingAccount",
						Name:       "acme",
						UID:        "account-uid",
						Controller: ptr.To(true),
					}},
				},
			},
			wantErr: true,
		},
		{
			name: "uid mismatch rejected",
			invoice: &billingv1alpha1.Invoice{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: billingv1alpha1.GroupVersion.String(),
						Kind:       "BillingAccount",
						Name:       "acme",
						UID:        "other-uid",
					}},
				},
			},
			wantErr: true,
		},
		{
			name: "wrong kind ignored then missing",
			invoice: &billingv1alpha1.Invoice{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "v1",
						Kind:       "Namespace",
						Name:       "acme",
						UID:        "account-uid",
					}},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateInvoiceOwnerReference(tt.invoice, account)
			if tt.wantErr && len(errs) == 0 {
				t.Fatal("expected error, got none")
			}
			if !tt.wantErr && len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateInvoiceCreateAndUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}

	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "acme",
			Namespace: "default",
			UID:       types.UID("account-uid"),
		},
		Spec: billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build()

	start := metav1.NewTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	end := metav1.NewTime(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	laterEnd := metav1.NewTime(time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC))

	validInvoice := &billingv1alpha1.Invoice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "acme-2026-06",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: billingv1alpha1.GroupVersion.String(),
				Kind:       "BillingAccount",
				Name:       "acme",
				UID:        "account-uid",
				Controller: ptr.To(false),
			}},
		},
		Spec: billingv1alpha1.InvoiceSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Period:            billingv1alpha1.InvoicePeriod{Start: start, End: end},
		},
	}

	t.Run("create succeeds", func(t *testing.T) {
		errs := ValidateInvoiceCreate(context.Background(), c, validInvoice)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
	})

	t.Run("create rejects missing account", func(t *testing.T) {
		invoice := validInvoice.DeepCopy()
		invoice.Spec.BillingAccountRef.Name = "missing"
		invoice.OwnerReferences[0].Name = "missing"
		errs := ValidateInvoiceCreate(context.Background(), c, invoice)
		if len(errs) == 0 {
			t.Fatal("expected error for missing billing account")
		}
	})

	t.Run("create rejects inverted period", func(t *testing.T) {
		invoice := validInvoice.DeepCopy()
		invoice.Spec.Period.Start = end
		invoice.Spec.Period.End = start
		errs := ValidateInvoiceCreate(context.Background(), c, invoice)
		if len(errs) == 0 {
			t.Fatal("expected period ordering error")
		}
	})

	t.Run("create rejects missing ownerReference", func(t *testing.T) {
		invoice := validInvoice.DeepCopy()
		invoice.OwnerReferences = nil
		errs := ValidateInvoiceCreate(context.Background(), c, invoice)
		if len(errs) == 0 {
			t.Fatal("expected ownerReference error")
		}
	})

	t.Run("update rejects period mutation", func(t *testing.T) {
		updated := validInvoice.DeepCopy()
		updated.Spec.Period.End = laterEnd
		errs := ValidateInvoiceUpdate(context.Background(), c, validInvoice, updated)
		if len(errs) == 0 {
			t.Fatal("expected period immutability error")
		}
	})

	t.Run("update rejects billingAccountRef mutation", func(t *testing.T) {
		updated := validInvoice.DeepCopy()
		updated.Spec.BillingAccountRef.Name = "other"
		errs := ValidateInvoiceUpdate(context.Background(), c, validInvoice, updated)
		if len(errs) == 0 {
			t.Fatal("expected billingAccountRef immutability error")
		}
	})

	t.Run("update succeeds when unchanged", func(t *testing.T) {
		updated := validInvoice.DeepCopy()
		errs := ValidateInvoiceUpdate(context.Background(), c, validInvoice, updated)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
	})
}
