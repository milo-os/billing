// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const displayNameAnnotation = "kubernetes.io/display-name"

// openMeterInvoiceAnnotation marks an Invoice projected by openmeter-provider
// from an OpenMeter invoice, which is how invoicing works now. Invoices
// without it predate it.
const openMeterInvoiceAnnotation = "openmeter.miloapis.com/invoiceId"

// orgBillingPath is the billing API path for an organization's namespace,
// read through its control plane.
func orgBillingPath(org string) string {
	return Scope{Org: org}.ControlPlane() + "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-" + org
}

// ProjectBillingAccount returns the billing account a project is bound to,
// or nil when it has no active binding.
func (c *Client) ProjectBillingAccount(ctx context.Context, org, project string) (*billingv1alpha1.BillingAccount, error) {
	var bindings billingv1alpha1.BillingAccountBindingList
	if err := c.Do(ctx, http.MethodGet, orgBillingPath(org)+"/billingaccountbindings", nil, &bindings); err != nil {
		return nil, err
	}
	name := ""
	for _, b := range bindings.Items {
		if b.Spec.ProjectRef.Name == project &&
			(b.Status.Phase == "" || b.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive) {
			name = b.Spec.BillingAccountRef.Name
			break
		}
	}
	if name == "" {
		return nil, nil
	}
	var account billingv1alpha1.BillingAccount
	if err := c.Do(ctx, http.MethodGet, orgBillingPath(org)+"/billingaccounts/"+name, nil, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

// ListPaymentMethods returns a billing account's payment methods.
func (c *Client) ListPaymentMethods(ctx context.Context, org, account string) ([]billingv1alpha1.PaymentMethod, error) {
	var list billingv1alpha1.PaymentMethodList
	if err := c.Do(ctx, http.MethodGet, orgBillingPath(org)+"/paymentmethods", nil, &list); err != nil {
		return nil, err
	}
	var out []billingv1alpha1.PaymentMethod
	for _, pm := range list.Items {
		if pm.Spec.BillingAccountRef.Name == account && pm.DeletionTimestamp == nil {
			out = append(out, pm)
		}
	}
	return out, nil
}

// ListInvoices returns a billing account's invoices.
func (c *Client) ListInvoices(ctx context.Context, org, account string) ([]billingv1alpha1.Invoice, error) {
	var list billingv1alpha1.InvoiceList
	if err := c.Do(ctx, http.MethodGet, orgBillingPath(org)+"/invoices", nil, &list); err != nil {
		return nil, err
	}
	var out []billingv1alpha1.Invoice
	for _, inv := range list.Items {
		if inv.Spec.BillingAccountRef.Name == account {
			out = append(out, inv)
		}
	}
	return out, nil
}

// AccountPlan returns the name of the Offer a billing account is on, or ""
// when it can't be read.
func (c *Client) AccountPlan(ctx context.Context, org, account string) string {
	var list billingv1alpha1.BillingEntitlementList
	if err := c.Do(ctx, http.MethodGet, orgBillingPath(org)+"/billingentitlements", nil, &list); err != nil {
		return ""
	}
	for _, e := range list.Items {
		if e.Spec.BillingAccountRef.Name == account {
			return e.Spec.OfferRef.Name
		}
	}
	return ""
}

// AccountSummary is what the assistant may know about a billing account.
// Contact details, postal addresses and tax IDs are deliberately left out.
type AccountSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	// Phase is Provisioning, Ready, Suspended or Archived.
	Phase string `json:"phase"`
	// Status explains the phase in the account's own words.
	Status string `json:"status,omitempty"`
	// Issues lists anything the account reports as not right, e.g. a default
	// payment method that isn't active.
	Issues       []string          `json:"issues,omitempty"`
	Currency     string            `json:"currency"`
	PaymentTerms *TermsSummary     `json:"paymentTerms,omitempty"`
	CurrentCycle *Period           `json:"currentCycle,omitempty"`
	Plan         string            `json:"plan,omitempty"`
	Arrangement  *ArrangementTerms `json:"invoiceArrangement,omitempty"`
	// DefaultPaymentMethod is what invoices are charged to.
	DefaultPaymentMethod *PaymentMethodSummary `json:"defaultPaymentMethod,omitempty"`
	LinkedProjects       int32                 `json:"linkedProjects"`
	LatestInvoice        string                `json:"latestInvoice,omitempty"`
}

// TermsSummary is when the account is invoiced and how long it has to pay.
type TermsSummary struct {
	InvoiceFrequency  string `json:"invoiceFrequency,omitempty"`
	InvoiceDayOfMonth int32  `json:"invoiceDayOfMonth,omitempty"`
	NetDays           int32  `json:"netDays,omitempty"`
}

// ArrangementTerms is a negotiated arrangement to pay by invoice.
type ArrangementTerms struct {
	Type        string `json:"type"`
	NetDays     int32  `json:"netDays,omitempty"`
	CreditLimit string `json:"creditLimit,omitempty"`
	StartsAt    string `json:"startsAt,omitempty"`
	EndsAt      string `json:"endsAt,omitempty"`
}

// PaymentMethodSummary is what the assistant may know about a payment
// method: enough to recognise it, never enough to use it.
type PaymentMethodSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	// Type is card or usBankAccount.
	Type string `json:"type,omitempty"`
	// Status is Pending, AwaitingConfirmation, Active or Failed.
	Status      string       `json:"status"`
	Default     bool         `json:"default"`
	Card        *CardSummary `json:"card,omitempty"`
	BankAccount *BankSummary `json:"bankAccount,omitempty"`
	// Problem says why a payment method failed, when it did.
	Problem string `json:"problem,omitempty"`
}

// CardSummary identifies a card without its number.
type CardSummary struct {
	Brand   string `json:"brand"`
	Last4   string `json:"last4"`
	Expiry  string `json:"expiry"`
	Expired bool   `json:"expired,omitempty"`
}

// BankSummary identifies a bank account without its number.
type BankSummary struct {
	BankName    string `json:"bankName,omitempty"`
	Last4       string `json:"last4,omitempty"`
	AccountType string `json:"accountType,omitempty"`
}

// InvoiceSummary is one invoice.
type InvoiceSummary struct {
	Name        string `json:"name"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
	// Status is Open, Paid, PastDue or Void.
	Status     string `json:"status"`
	Total      string `json:"total,omitempty"`
	AmountPaid string `json:"amountPaid,omitempty"`
	AmountDue  string `json:"amountDue,omitempty"`
	DueDate    string `json:"dueDate,omitempty"`
	PaidAt     string `json:"paidAt,omitempty"`
	Overdue    bool   `json:"overdue,omitempty"`
	// Link is the invoice's Stripe-hosted page, to view, download or pay it.
	Link string `json:"link,omitempty"`
	// Problem is anything the invoicing provider reports as wrong with the
	// invoice, e.g. a currency that doesn't match the account's.
	Problem string `json:"problem,omitempty"`
}

// SummarizeAccount describes a billing account. defaultPM is its default
// payment method, when it could be read.
func SummarizeAccount(a *billingv1alpha1.BillingAccount, plan string, defaultPM *billingv1alpha1.PaymentMethod, now time.Time) AccountSummary {
	s := AccountSummary{
		Name:           a.Name,
		DisplayName:    a.Annotations[displayNameAnnotation],
		Phase:          string(a.Status.Phase),
		Currency:       a.Spec.CurrencyCode,
		Plan:           plan,
		LinkedProjects: a.Status.LinkedProjectsCount,
	}
	for _, c := range a.Status.Conditions {
		if c.Type == "Ready" {
			s.Status = c.Message
		}
		if c.Status != metav1.ConditionTrue && c.Message != "" {
			s.Issues = append(s.Issues, c.Message)
		}
	}
	if t := a.Spec.PaymentTerms; t != nil {
		s.PaymentTerms = &TermsSummary{InvoiceFrequency: t.InvoiceFrequency, InvoiceDayOfMonth: t.InvoiceDayOfMonth, NetDays: t.NetDays}
	}
	current, _ := BillingCycles(a.Spec.PaymentTerms, now)
	s.CurrentCycle = &Period{Name: current.Name, Start: current.Start, End: current.End, Label: RangeLabel(current.Start, current.PeriodEnd)}
	if arr := a.Status.PaymentArrangement; arr != nil {
		terms := &ArrangementTerms{Type: string(arr.Type), StartsAt: dateOf(arr.StartsAt), EndsAt: dateOf(arr.EndsAt)}
		if arr.Invoice != nil {
			terms.NetDays = arr.Invoice.NetDays
			terms.CreditLimit = arr.Invoice.CreditLimit
		}
		s.Arrangement = terms
	}
	if defaultPM != nil {
		pm := SummarizePaymentMethod(*defaultPM, defaultPM.Name, now)
		s.DefaultPaymentMethod = &pm
	}
	if a.Status.LatestInvoiceRef != nil {
		s.LatestInvoice = a.Status.LatestInvoiceRef.Name
	}
	return s
}

// SummarizePaymentMethod describes a payment method, leaving out anything
// beyond what identifies it.
func SummarizePaymentMethod(pm billingv1alpha1.PaymentMethod, defaultName string, now time.Time) PaymentMethodSummary {
	s := PaymentMethodSummary{
		Name:        pm.Name,
		DisplayName: pm.Spec.DisplayName,
		Status:      string(pm.Status.Phase),
		Default:     pm.Name == defaultName,
	}
	if d := pm.Status.Details; d != nil {
		s.Type = string(d.Type)
		if card := d.Card; card != nil {
			s.Card = &CardSummary{
				Brand:  card.Brand,
				Last4:  card.Last4,
				Expiry: fmt.Sprintf("%02d/%d", card.ExpiryMonth, card.ExpiryYear),
				// A card is good through the last day of its expiry month.
				Expired: card.ExpiryYear > 0 &&
					now.After(time.Date(int(card.ExpiryYear), time.Month(card.ExpiryMonth)+1, 1, 0, 0, 0, 0, time.UTC)),
			}
		}
		if bank := d.USBankAccount; bank != nil {
			s.BankAccount = &BankSummary{BankName: bank.BankName, Last4: bank.Last4, AccountType: bank.AccountType}
		}
	}
	if pm.Status.Phase == billingv1alpha1.PaymentMethodPhaseFailed {
		s.Problem = strings.TrimSpace(pm.Status.FailureMessage)
		if s.Problem == "" {
			s.Problem = pm.Status.FailureReason
		}
	}
	return s
}

// SummarizeInvoices describes the invoices openmeter-provider issued, newest
// first, keeping only those in status when it is set, and at most limit of
// them.
func SummarizeInvoices(invoices []billingv1alpha1.Invoice, status string, limit int, now time.Time) []InvoiceSummary {
	sort.SliceStable(invoices, func(i, j int) bool {
		return invoices[i].Spec.Period.Start.After(invoices[j].Spec.Period.Start.Time)
	})
	out := []InvoiceSummary{}
	for _, inv := range invoices {
		if inv.Annotations[openMeterInvoiceAnnotation] == "" {
			continue
		}
		if status != "" && !strings.EqualFold(string(inv.Status.Phase), status) {
			continue
		}
		if limit > 0 && len(out) == limit {
			break
		}
		st := inv.Status
		s := InvoiceSummary{
			Name:        inv.Name,
			PeriodStart: dateOf(&inv.Spec.Period.Start),
			PeriodEnd:   dateOf(&inv.Spec.Period.End),
			Status:      string(st.Phase),
			Total:       moneyDisplay(st.Total, st.CurrencyCode),
			AmountPaid:  moneyDisplay(st.AmountPaid, st.CurrencyCode),
			AmountDue:   moneyDisplay(st.AmountDue, st.CurrencyCode),
			DueDate:     dateOf(st.DueDate),
			PaidAt:      dateOf(st.PaidAt),
			Overdue: st.Phase == billingv1alpha1.InvoicePhasePastDue ||
				(st.Phase == billingv1alpha1.InvoicePhaseOpen && st.DueDate != nil && now.After(st.DueDate.Time)),
			// The provider keeps this link fresh, so it always opens.
			Link: st.DocumentURI,
		}
		for _, c := range st.Conditions {
			if c.Type == "Ready" && (c.Status != metav1.ConditionTrue || c.Reason == "CurrencyMismatch" || c.Reason == "NotFoundInOpenMeter") {
				s.Problem = c.Message
			}
		}
		out = append(out, s)
	}
	return out
}

func moneyDisplay(amount, currency string) string {
	if amount == "" {
		return ""
	}
	return FormatMoney(amount, currency)
}

func dateOf(t *metav1.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.DateOnly)
}
