// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func invoiceRequest(account string, mode billingv1alpha1.InvoiceRequestMode) *billingv1alpha1.InvoiceRequest {
	return &billingv1alpha1.InvoiceRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-first-invoice", Namespace: "org-acme"},
		Spec: billingv1alpha1.InvoiceRequestSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: account},
			Mode:              mode,
		},
	}
}

func TestValidateInvoiceRequestCreate(t *testing.T) {
	now := metav1.Now()
	accounts := []client.Object{
		&billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "org-acme"}},
		&billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{
			Name: "closing", Namespace: "org-acme", DeletionTimestamp: &now, Finalizers: []string{"test-finalizer"},
		}},
		&billingv1alpha1.BillingAccount{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "org-other"}},
	}

	tests := []struct {
		name    string
		account string
		mode    billingv1alpha1.InvoiceRequestMode
		wantErr bool
	}{
		{name: "preview", account: "acme", mode: billingv1alpha1.InvoiceRequestModePreview},
		{name: "issue", account: "acme", mode: billingv1alpha1.InvoiceRequestModeIssue},
		{name: "no account named", account: "", mode: billingv1alpha1.InvoiceRequestModePreview, wantErr: true},
		{name: "missing account", account: "nope", mode: billingv1alpha1.InvoiceRequestModeIssue, wantErr: true},
		{name: "account in another namespace", account: "elsewhere", mode: billingv1alpha1.InvoiceRequestModeIssue, wantErr: true},
		{name: "account being deleted", account: "closing", mode: billingv1alpha1.InvoiceRequestModeIssue, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(accounts...).Build()
			errs := ValidateInvoiceRequestCreate(context.Background(), c, invoiceRequest(tt.account, tt.mode))
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("ValidateInvoiceRequestCreate() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateInvoiceRequestUpdate(t *testing.T) {
	old := invoiceRequest("acme", billingv1alpha1.InvoiceRequestModePreview)

	if errs := ValidateInvoiceRequestUpdate(old, invoiceRequest("acme", billingv1alpha1.InvoiceRequestModePreview)); len(errs) > 0 {
		t.Errorf("unchanged spec: errors = %v", errs)
	}
	if errs := ValidateInvoiceRequestUpdate(old, invoiceRequest("acme", billingv1alpha1.InvoiceRequestModeIssue)); len(errs) == 0 {
		t.Error("turning a preview into an issue: want an error")
	}
	if errs := ValidateInvoiceRequestUpdate(old, invoiceRequest("other", billingv1alpha1.InvoiceRequestModePreview)); len(errs) == 0 {
		t.Error("changing the billing account: want an error")
	}
}
