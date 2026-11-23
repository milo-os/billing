// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func chargeDraft(lines ...billingv1alpha1.ChargeDraftLine) *billingv1alpha1.ChargeDraft {
	return &billingv1alpha1.ChargeDraft{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-charges", Namespace: "org-acme"},
		Spec: billingv1alpha1.ChargeDraftSpec{
			BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "acme"},
			Lines:             lines,
			Invoice:           billingv1alpha1.ChargeInvoiceNow,
		},
	}
}

func customLine(description, quantity, unitAmount string) billingv1alpha1.ChargeDraftLine {
	return billingv1alpha1.ChargeDraftLine{Description: description, Quantity: quantity, UnitAmount: unitAmount, Currency: "USD"}
}

func TestValidateChargeDraft(t *testing.T) {
	oneTime := chargeTestPricing("cross-connect-install-v1", billingv1alpha1.ChargeTypeOneTime, "300")
	usage := chargeTestPricing("egress-v1", billingv1alpha1.ChargeTypeUsage, "0.05")

	tests := []struct {
		name    string
		draft   *billingv1alpha1.ChargeDraft
		account *billingv1alpha1.BillingAccount
		wantErr []string
	}{
		{name: "two custom lines", draft: chargeDraft(customLine("Node", "10", "25.00"), customLine("Control node", "10", "4.50"))},
		{name: "a catalog line left to its defaults", draft: chargeDraft(billingv1alpha1.ChargeDraftLine{
			ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: oneTime.Name}, Quantity: "1",
		})},
		{name: "an account not on the invoice writer", draft: chargeDraft(customLine("Node", "1", "10")),
			account: chargeTestAccount(false), wantErr: []string{"charges aren't available"}},
		{name: "problems on each line, at that line", draft: chargeDraft(
			customLine("Node", "3.33", "0.15"),
			billingv1alpha1.ChargeDraftLine{ServicePricingRef: &billingv1alpha1.ChargeServicePricingRef{Name: usage.Name}, Quantity: "1"},
		), wantErr: []string{"spec.lines[0].quantity", "isn't whole cents", "spec.lines[1].servicePricingRef", "needs a OneTime or Recurring pricing"}},
		{name: "a line with a period that ends before it starts", draft: func() *billingv1alpha1.ChargeDraft {
			l := customLine("Node", "1", "10")
			l.ServicePeriod = &billingv1alpha1.ServicePeriod{Start: "2026-11-22", End: "2026-11-15"}
			return chargeDraft(l)
		}(), wantErr: []string{"spec.lines[0].servicePeriod", "can't end before it starts"}},
		{name: "no lines", draft: chargeDraft(), wantErr: []string{"at least one line"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := tt.account
			if account == nil {
				account = chargeTestAccount(true)
			}
			c := chargeTestClient(t, []client.Object{account, oneTime, usage}...)
			before := tt.draft.DeepCopy()
			errs := ValidateChargeDraft(context.Background(), c, tt.draft, chargeNow)
			if len(tt.wantErr) == 0 {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
			} else {
				got := errs.ToAggregate().Error()
				for _, want := range tt.wantErr {
					if !strings.Contains(got, want) {
						t.Errorf("errors %q don't mention %q", got, want)
					}
				}
			}
			if tt.draft.Spec.Lines != nil && before.Spec.Lines[0].ServicePricingRef != nil &&
				tt.draft.Spec.Lines[0].ServicePricingRef.Namespace != before.Spec.Lines[0].ServicePricingRef.Namespace {
				t.Error("validation changed the draft")
			}
		})
	}
}

func TestValidateInvoiceRequestChargeDraft(t *testing.T) {
	draft := chargeDraft(customLine("Node", "1", "10"))
	other := chargeDraft(customLine("Node", "1", "10"))
	other.Name, other.Spec.BillingAccountRef.Name = "other-charges", "other"
	c := chargeTestClient(t, chargeTestAccount(true), draft, other)

	for _, tt := range []struct {
		name, draft, wantErr string
	}{
		{name: "the account's draft", draft: "acme-charges"},
		{name: "a missing draft", draft: "gone", wantErr: "Not found"},
		{name: "another account's draft", draft: "other-charges", wantErr: "is for billing account \"other\""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := invoiceRequest("acme", billingv1alpha1.InvoiceRequestModePreview)
			req.Spec.ChargeDraftRef = &billingv1alpha1.ChargeDraftReference{Name: tt.draft}
			errs := ValidateInvoiceRequestCreate(context.Background(), c, req)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if !strings.Contains(errs.ToAggregate().Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", errs, tt.wantErr)
			}
		})
	}
}
