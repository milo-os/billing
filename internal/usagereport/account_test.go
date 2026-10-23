// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

var accountNow = time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)

func card(name string, month, year int32, phase billingv1alpha1.PaymentMethodPhase) billingv1alpha1.PaymentMethod {
	pm := billingv1alpha1.PaymentMethod{}
	pm.Name = name
	pm.Spec.DisplayName = "Company card"
	pm.Status.Phase = phase
	pm.Status.Details = &billingv1alpha1.PaymentMethodDetails{
		Type: billingv1alpha1.PaymentMethodInstrumentTypeCard,
		Card: &billingv1alpha1.PaymentMethodCardDetails{
			Brand: "visa", Last4: "4242", ExpiryMonth: month, ExpiryYear: year,
			IssuerIdentificationNumber: "424242", Country: "US",
		},
	}
	return pm
}

func TestSummarizePaymentMethod(t *testing.T) {
	s := SummarizePaymentMethod(card("pm-1", 10, 2026, billingv1alpha1.PaymentMethodPhaseActive), "pm-1", accountNow)
	if !s.Default || s.Card.Expiry != "10/2026" || s.Card.Expired || s.Card.Brand != "visa" {
		t.Errorf("summary = %+v %+v", s, s.Card)
	}
	// Good through the last day of its expiry month.
	if s := SummarizePaymentMethod(card("pm-2", 9, 2026, billingv1alpha1.PaymentMethodPhaseActive), "pm-1", accountNow); !s.Card.Expired || s.Default {
		t.Errorf("a card that expired in September should be expired and not default: %+v %+v", s, s.Card)
	}

	failed := card("pm-3", 1, 2030, billingv1alpha1.PaymentMethodPhaseFailed)
	failed.Status.FailureReason = "card_declined"
	failed.Status.FailureMessage = "Your card was declined."
	if s := SummarizePaymentMethod(failed, "", accountNow); s.Problem != "Your card was declined." {
		t.Errorf("problem = %q", s.Problem)
	}
}

func invoice(name, provider string, start time.Time, phase billingv1alpha1.InvoicePhase, due *time.Time) billingv1alpha1.Invoice {
	inv := billingv1alpha1.Invoice{}
	inv.Name = name
	if provider != "" {
		inv.Annotations = map[string]string{openMeterInvoiceAnnotation: provider}
	}
	inv.Spec.Period.Start = metav1.NewTime(start)
	inv.Spec.Period.End = metav1.NewTime(start.AddDate(0, 1, 0))
	inv.Status.Phase = phase
	inv.Status.CurrencyCode = "USD"
	inv.Status.Total = "12.50"
	inv.Status.AmountDue = "12.50"
	inv.Status.AmountPaid = "0"
	inv.Status.DocumentURI = "https://invoice.stripe.com/i/acct_1/test_" + name
	if due != nil {
		d := metav1.NewTime(*due)
		inv.Status.DueDate = &d
	}
	return inv
}

func TestSummarizeInvoices(t *testing.T) {
	month := func(m time.Month) time.Time { return time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC) }
	pastDue := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	invoices := []billingv1alpha1.Invoice{
		invoice("acme-2026-08", "", month(time.August), billingv1alpha1.InvoicePhasePastDue, nil), // before OpenMeter
		invoice("acme-2026-09", "inv_9", month(time.September), billingv1alpha1.InvoicePhaseOpen, &pastDue),
		invoice("acme-2026-10", "inv_10", month(time.October), billingv1alpha1.InvoicePhaseOpen, nil),
	}
	mismatch := invoice("acme-2026-07", "inv_7", month(time.July), billingv1alpha1.InvoicePhaseOpen, nil)
	mismatch.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "CurrencyMismatch",
		Message: "Invoice currency EUR does not match the billing account's USD.",
	}}
	invoices = append(invoices, mismatch)

	got := SummarizeInvoices(invoices, "", 0, accountNow)
	if len(got) != 3 || got[0].Name != "acme-2026-10" || got[1].Name != "acme-2026-09" || got[2].Name != "acme-2026-07" {
		t.Fatalf("invoices = %+v, want the OpenMeter ones newest first", got)
	}
	if !got[1].Overdue || got[0].Overdue {
		t.Errorf("only the September invoice is past its due date: %+v", got)
	}
	if got[0].Total != "$12.50" || got[0].Link != "https://invoice.stripe.com/i/acct_1/test_acme-2026-10" {
		t.Errorf("invoice = %+v", got[0])
	}
	if got[2].Problem == "" {
		t.Errorf("a currency mismatch should be reported: %+v", got[2])
	}

	if got := SummarizeInvoices(invoices, "open", 1, accountNow); len(got) != 1 || got[0].Name != "acme-2026-10" {
		t.Errorf("status and limit: %+v", got)
	}
}

func TestSummarizeAccount(t *testing.T) {
	a := &billingv1alpha1.BillingAccount{}
	a.Name = "acme-main"
	a.Annotations = map[string]string{displayNameAnnotation: "Acme"}
	a.Spec.CurrencyCode = "USD"
	a.Spec.PaymentTerms = &billingv1alpha1.PaymentTerms{InvoiceFrequency: "Monthly", InvoiceDayOfMonth: 15, NetDays: 30}
	a.Status.Phase = billingv1alpha1.BillingAccountPhaseSuspended
	a.Status.Conditions = []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Message: "Billing account is suspended."},
		{Type: "DefaultPaymentMethodReady", Status: metav1.ConditionFalse, Message: "Default payment method failed."},
		{Type: "InvoicingReady", Status: metav1.ConditionTrue, Message: "Invoices are current."},
	}
	pm := card("pm-1", 10, 2030, billingv1alpha1.PaymentMethodPhaseFailed)

	s := SummarizeAccount(a, "payg-4", &pm, accountNow)
	if s.DisplayName != "Acme" || s.Phase != "Suspended" || s.Status != "Billing account is suspended." || s.Plan != "payg-4" {
		t.Errorf("summary = %+v", s)
	}
	if len(s.Issues) != 2 {
		t.Errorf("issues = %v, want the two failing conditions", s.Issues)
	}
	if s.PaymentTerms.NetDays != 30 || s.CurrentCycle.Label != "15 Oct - 15 Nov 2026" {
		t.Errorf("terms = %+v, cycle = %+v", s.PaymentTerms, s.CurrentCycle)
	}
	if s.DefaultPaymentMethod == nil || !s.DefaultPaymentMethod.Default || s.DefaultPaymentMethod.Card.Last4 != "4242" {
		t.Errorf("default payment method = %+v", s.DefaultPaymentMethod)
	}
}
