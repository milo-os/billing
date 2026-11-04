// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// Every event rule's match must compile to a bool and every summary block to
// a string, which is what the activity apiserver checks when the policy is
// applied.
func TestEventRulesCompile(t *testing.T) {
	paths, err := filepath.Glob(eventPolicyDir + "*-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env := eventEnv(t, nil)
	for _, path := range paths {
		p := loadEventPolicy(t, filepath.Base(path))
		if len(p.Spec.EventRules) == 0 {
			continue
		}
		for _, r := range p.Spec.EventRules {
			t.Run(filepath.Base(path)+"/"+r.Name, func(t *testing.T) {
				ast, iss := env.Compile(r.Match)
				if iss != nil && iss.Err() != nil {
					t.Fatalf("compile match: %v", iss.Err())
				}
				if !ast.OutputType().IsExactType(cel.BoolType) {
					t.Errorf("match returns %v, want bool", ast.OutputType())
				}
				if strings.Count(r.Summary, "{{") != strings.Count(r.Summary, "}}") {
					t.Fatalf("unbalanced template delimiters")
				}
				for _, m := range summaryBlockRE.FindAllStringSubmatch(r.Summary, -1) {
					ast, iss := env.Compile(m[1])
					if iss != nil && iss.Err() != nil {
						t.Fatalf("compile {{ %s }}: %v", m[1], iss.Err())
					}
					if !ast.OutputType().IsExactType(cel.StringType) && !ast.OutputType().IsExactType(cel.DynType) {
						t.Errorf("{{ %s }} returns %v, want string", m[1], ast.OutputType())
					}
				}
			})
		}
	}
}

// Policies match on the reasons billing defines, so a renamed constant must
// not silently orphan a rule.
func TestEventRulesUseDefinedReasons(t *testing.T) {
	for policy, reasons := range map[string][]string{
		"invoice-policy.yaml": {
			billingv1alpha1.EventReasonInvoiceIssued,
			billingv1alpha1.EventReasonInvoicePaymentSucceeded,
			billingv1alpha1.EventReasonInvoicePaymentFailed,
			billingv1alpha1.EventReasonInvoicePaymentActionRequired,
			billingv1alpha1.EventReasonInvoicePartiallyPaid,
			billingv1alpha1.EventReasonInvoicePastDue,
			billingv1alpha1.EventReasonInvoiceVoided,
			billingv1alpha1.EventReasonInvoiceCurrencyMismatch,
		},
		"paymentmethod-policy.yaml": {
			billingv1alpha1.EventReasonPaymentMethodAttached,
			billingv1alpha1.EventReasonPaymentMethodFailed,
			billingv1alpha1.EventReasonPaymentMethodExpiringSoon,
			billingv1alpha1.EventReasonPaymentMethodExpired,
		},
		"billingaccount-policy.yaml": {
			billingv1alpha1.EventReasonDefaultPaymentMethodChanged,
			billingv1alpha1.EventReasonInvoicingPastDue,
			billingv1alpha1.EventReasonInvoicingCurrent,
		},
		"billingarrangement-policy.yaml": {
			billingv1alpha1.EventReasonPaymentTermsEnded,
		},
	} {
		p := loadEventPolicy(t, policy)
		for _, reason := range reasons {
			// Every reason renders even without annotations, through its
			// fallback.
			rule, summary, _ := summarizeEvent(t, p, withoutAnnotations(billingEvent("Any", "obj-1", reason, nil)))
			if rule == "" || summary == "" {
				t.Errorf("%s: no rule matches %s", policy, reason)
			}
		}
	}
}

func invoiceEvent(reason string, detail map[string]string) map[string]any {
	base := map[string]string{
		"billing-account": "acme",
		"period-start":    "2026-10-01T00:00:00Z",
		"currency":        "USD",
	}
	for k, v := range detail {
		base[k] = v
	}
	return billingEvent("Invoice", "acme-2026-10", reason, base)
}

func TestInvoiceEventSummaries(t *testing.T) {
	p := loadEventPolicy(t, "invoice-policy.yaml")

	for name, tc := range map[string]struct {
		event map[string]any
		rule  string
		want  string
	}{
		"issued": {
			invoiceEvent(billingv1alpha1.EventReasonInvoiceIssued, map[string]string{"amount": "482.19", "due-date": "2026-11-30T00:00:00Z"}),
			"issued", "The October 2026 invoice for billing account acme was issued: USD 482.19, due 30 November 2026",
		},
		"issued without a due date": {
			invoiceEvent(billingv1alpha1.EventReasonInvoiceIssued, map[string]string{"amount": "482.19"}),
			"issued", "The October 2026 invoice for billing account acme was issued: USD 482.19",
		},
		"paid": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentSucceeded, map[string]string{"amount": "482.19"}),
			"payment-succeeded", "The October 2026 invoice for billing account acme was paid: USD 482.19",
		},
		"declined with a retry": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentFailed, map[string]string{
				"amount": "482.19", "decline-reason": "InsufficientFunds", "next-attempt": "2026-11-05T09:30:00Z",
			}),
			"payment-failed", "Payment of USD 482.19 for the October 2026 invoice for billing account acme was declined (insufficient funds). Next attempt on 5 November 2026",
		},
		"processing error, no retry": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentFailed, map[string]string{"amount": "482.19", "decline-reason": "ProcessingError"}),
			"payment-failed", "Payment of USD 482.19 for the October 2026 invoice for billing account acme did not go through (a processing error occurred)",
		},
		"unknown decline reason": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentFailed, map[string]string{"amount": "482.19", "decline-reason": "do_not_honor"}),
			"payment-failed", "Payment of USD 482.19 for the October 2026 invoice for billing account acme was declined",
		},
		"no decline reason": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentFailed, map[string]string{"amount": "482.19"}),
			"payment-failed", "Payment of USD 482.19 for the October 2026 invoice for billing account acme was declined",
		},
		"action required": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentActionRequired, map[string]string{"amount": "482.19"}),
			"payment-action-required", "Payment of USD 482.19 for the October 2026 invoice for billing account acme needs the card holder to confirm it",
		},
		"partially paid": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePartiallyPaid, map[string]string{"amount-paid": "200.00", "amount-due": "282.19"}),
			"partially-paid", "USD 200.00 was received for the October 2026 invoice for billing account acme; USD 282.19 is still due",
		},
		"past due": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePastDue, map[string]string{"amount-due": "482.19"}),
			"past-due", "The October 2026 invoice for billing account acme is past due: USD 482.19 outstanding",
		},
		"voided": {
			invoiceEvent(billingv1alpha1.EventReasonInvoiceVoided, nil),
			"voided", "The October 2026 invoice for billing account acme was voided",
		},
		"currency mismatch": {
			invoiceEvent(billingv1alpha1.EventReasonInvoiceCurrencyMismatch, map[string]string{"currency": "EUR"}),
			"currency-mismatch", "The October 2026 invoice for billing account acme is in EUR, which does not match the billing account's currency",
		},
		"paid without its detail": {
			withoutAnnotations(invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentSucceeded, nil)),
			"fallback", "Invoice acme-2026-10 was paid",
		},
		"paid without an amount": {
			invoiceEvent(billingv1alpha1.EventReasonInvoicePaymentSucceeded, nil),
			"fallback", "Invoice acme-2026-10 was paid",
		},
	} {
		t.Run(name, func(t *testing.T) {
			checkEventSummary(t, p, tc.event, tc.rule, tc.want)
		})
	}

	t.Run("links the invoice and the billing account", func(t *testing.T) {
		links := checkEventSummary(t, p,
			invoiceEvent(billingv1alpha1.EventReasonInvoicePastDue, map[string]string{"amount-due": "482.19"}),
			"past-due", "The October 2026 invoice for billing account acme is past due: USD 482.19 outstanding")
		checkLinks(t, links, map[string]string{
			"October 2026 invoice": "invoices/organization-acme/acme-2026-10",
			"acme":                 "billingaccounts/organization-acme/acme",
		})
	})

	t.Run("an event for another reason matches nothing", func(t *testing.T) {
		if rule, _, _ := summarizeEvent(t, p, invoiceEvent("SomethingElse", nil)); rule != "" {
			t.Errorf("matched %s", rule)
		}
	})
}

func cardEvent(reason string, detail map[string]string) map[string]any {
	base := map[string]string{
		"billing-account": "acme",
		"card-brand":      "visa",
		"card-last4":      "4242",
		"card-expiry":     "2026-11-01T00:00:00Z",
	}
	for k, v := range detail {
		base[k] = v
	}
	return billingEvent("PaymentMethod", "card-1", reason, base)
}

func TestPaymentMethodEventSummaries(t *testing.T) {
	p := loadEventPolicy(t, "paymentmethod-policy.yaml")
	noCard := billingEvent("PaymentMethod", "card-1", billingv1alpha1.EventReasonPaymentMethodAttached, map[string]string{"billing-account": "acme"})

	for name, tc := range map[string]struct {
		event map[string]any
		rule  string
		want  string
	}{
		"attached card": {
			cardEvent(billingv1alpha1.EventReasonPaymentMethodAttached, nil),
			"attached-card", "Visa ending 4242 was added to billing account acme and is ready to use",
		},
		"attached card of an unlisted brand": {
			cardEvent(billingv1alpha1.EventReasonPaymentMethodAttached, map[string]string{"card-brand": "elo"}),
			"attached-card", "Card ending 4242 was added to billing account acme and is ready to use",
		},
		"attached without card detail": {
			noCard, "attached", "Payment method card-1 was added to billing account acme and is ready to use",
		},
		"failed with a reason": {
			billingEvent("PaymentMethod", "card-1", billingv1alpha1.EventReasonPaymentMethodFailed,
				map[string]string{"billing-account": "acme", "decline-reason": "InsufficientFunds"}),
			"failed", "Payment method card-1 could not be added to billing account acme: the card was declined (insufficient funds)",
		},
		"failed with an unknown reason": {
			billingEvent("PaymentMethod", "card-1", billingv1alpha1.EventReasonPaymentMethodFailed,
				map[string]string{"billing-account": "acme", "decline-reason": "generic_decline"}),
			"failed", "Payment method card-1 could not be added to billing account acme: the card was declined",
		},
		"failed without a reason": {
			billingEvent("PaymentMethod", "card-1", billingv1alpha1.EventReasonPaymentMethodFailed,
				map[string]string{"billing-account": "acme"}),
			"failed", "Payment method card-1 could not be added to billing account acme",
		},
		"expiring soon": {
			cardEvent(billingv1alpha1.EventReasonPaymentMethodExpiringSoon, nil),
			"expiring-soon", "Visa ending 4242 on billing account acme expires at the end of November 2026",
		},
		"expired": {
			cardEvent(billingv1alpha1.EventReasonPaymentMethodExpired, map[string]string{"card-brand": "amex", "card-last4": "0005"}),
			"expired", "American Express ending 0005 on billing account acme has expired",
		},
		"expired without its detail": {
			withoutAnnotations(cardEvent(billingv1alpha1.EventReasonPaymentMethodExpired, nil)),
			"fallback", "Payment method card-1 has expired",
		},
	} {
		t.Run(name, func(t *testing.T) {
			checkEventSummary(t, p, tc.event, tc.rule, tc.want)
		})
	}

	t.Run("links the card and the billing account", func(t *testing.T) {
		links := checkEventSummary(t, p, cardEvent(billingv1alpha1.EventReasonPaymentMethodAttached, nil),
			"attached-card", "Visa ending 4242 was added to billing account acme and is ready to use")
		checkLinks(t, links, map[string]string{
			"Visa ending 4242": "paymentmethods/organization-acme/card-1",
			"acme":             "billingaccounts/organization-acme/acme",
		})
	})
}

func TestBillingAccountEventSummaries(t *testing.T) {
	p := loadEventPolicy(t, "billingaccount-policy.yaml")
	accountEvent := func(reason string, detail map[string]string) map[string]any {
		return billingEvent("BillingAccount", "acme", reason, detail)
	}

	for name, tc := range map[string]struct {
		event map[string]any
		rule  string
		want  string
	}{
		"billing makes a card the default": {
			accountEvent(billingv1alpha1.EventReasonDefaultPaymentMethodChanged, map[string]string{
				"payment-method": "card-1", "card-brand": "mastercard", "card-last4": "4444",
			}),
			"default-payment-method-card", "Mastercard ending 4444 is now the default payment method for billing account acme",
		},
		"billing makes a bank account the default": {
			accountEvent(billingv1alpha1.EventReasonDefaultPaymentMethodChanged, map[string]string{"payment-method": "bank-1"}),
			"default-payment-method", "Payment method bank-1 is now the default payment method for billing account acme",
		},
		"past due": {
			accountEvent(billingv1alpha1.EventReasonInvoicingPastDue, map[string]string{"invoice": "acme-2026-10"}),
			"past-due", "Billing account acme is past due on invoice acme-2026-10",
		},
		"past due without the invoice": {
			accountEvent(billingv1alpha1.EventReasonInvoicingPastDue, nil),
			"invoicing-status", "Billing account acme is past due",
		},
		"current again": {
			accountEvent(billingv1alpha1.EventReasonInvoicingCurrent, nil),
			"invoicing-status", "Billing account acme is no longer past due",
		},
	} {
		t.Run(name, func(t *testing.T) {
			checkEventSummary(t, p, tc.event, tc.rule, tc.want)
		})
	}
}

func TestBillingArrangementEventSummaries(t *testing.T) {
	p := loadEventPolicy(t, "billingarrangement-policy.yaml")
	checkEventSummary(t, p,
		billingEvent("BillingArrangement", "acme-terms", billingv1alpha1.EventReasonPaymentTermsEnded,
			map[string]string{"billing-account": "acme", "ends-at": "2027-09-30T00:00:00Z"}),
		"terms-ended", "Payment terms for billing account acme ended on 30 September 2027")
	checkEventSummary(t, p,
		withoutAnnotations(billingEvent("BillingArrangement", "acme-terms", billingv1alpha1.EventReasonPaymentTermsEnded, nil)),
		"terms-ended-fallback", "Payment terms acme-terms ended")
}

func TestBillingAccountDefaultPaymentMethodAudit(t *testing.T) {
	policy := policyNamed(t, "billing.miloapis.com-billingaccount")
	event := func(request any) map[string]any {
		audit := auditEvent("patch", 200, "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-acme/billingaccounts/acme", request)
		audit["objectRef"] = map[string]any{"name": "acme", "namespace": "organization-acme", "apiGroup": "billing.miloapis.com", "resource": "billingaccounts"}
		return audit
	}
	checkSummary(t, policy, "set the default payment method",
		event(map[string]any{"spec": map[string]any{"defaultPaymentMethodRef": map[string]any{"name": "card-1"}}}),
		"alice@example.com made payment method card-1 the default for billing account acme")
	checkSummary(t, policy, "change the default with other fields",
		event(map[string]any{"spec": map[string]any{"defaultPaymentMethodRef": map[string]any{"name": "card-1"}, "currencyCode": "USD"}}),
		"alice@example.com updated billing account acme")
	checkSummary(t, policy, "clear the default",
		event(map[string]any{"spec": map[string]any{"defaultPaymentMethodRef": nil}}),
		"alice@example.com updated billing account acme")
}
