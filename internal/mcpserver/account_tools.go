// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/usagereport"
)

// Account tool names. Patch offers them as "billing__<name>".
const (
	ToolGetBillingAccount  = "get_billing_account"
	ToolListPaymentMethods = "list_payment_methods"
	ToolListInvoices       = "list_invoices"
)

const (
	defaultInvoiceLimit = 12
	maxInvoiceLimit     = 50
)

// GetBillingAccountArgs are get_billing_account's arguments; it takes none.
type GetBillingAccountArgs struct{}

// ListPaymentMethodsArgs are list_payment_methods' arguments; it takes none.
type ListPaymentMethodsArgs struct{}

// ListInvoicesArgs are list_invoices' arguments.
type ListInvoicesArgs struct {
	Status string `json:"status,omitempty" jsonschema:"Only invoices in this status: Open, Paid, PastDue or Void."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Most invoices to return, newest first. Defaults to 12, at most 50."`
}

func registerAccountTools(server *mcp.Server, t *tools) {
	addTool(server, &mcp.Tool{
		Name: ToolGetBillingAccount,
		Description: "The billing account this project is billed to: whether it is ready or suspended and why, " +
			"its currency, plan, payment terms and current billing cycle, any invoice arrangement, and the default " +
			"payment method invoices are charged to.",
		Annotations: readOnly(),
	}, t.getBillingAccount)

	addTool(server, &mcp.Tool{
		Name: ToolListPaymentMethods,
		Description: "The payment methods on this project's billing account: card brand, last four digits and " +
			"expiry, or bank account, with each one's status, any failure, and which is the default.",
		Annotations: readOnly(),
	}, t.listPaymentMethods)

	addTool(server, &mcp.Tool{
		Name: ToolListInvoices,
		Description: "Invoices for this project's billing account, newest first: the period, status (Open, Paid, " +
			"PastDue, Void), total, amount paid and due, due date, and a link to view or pay it when there is one.",
		Annotations: readOnly(),
	}, t.listInvoices)
}

// accountContext is the billing account a tool reads, with how to reach it.
type accountContext struct {
	caller  *caller
	org     string
	account *billingv1alpha1.BillingAccount
}

// errNoAccount is the tool error for a project with no billing account.
const errNoAccount = "This project isn't bound to a billing account, so there is no billing account, payment " +
	"method or invoice to show. An organization owner can bind it to one on the organization's Billing page."

// account finds the billing account the conversation's project is bound to.
// A nil account with no error means the project has none; the returned
// string is a tool error message when something failed.
func (t *tools) account(ctx context.Context) (*accountContext, string) {
	c, err := t.resolve(ctx)
	if err != nil {
		return nil, err.Error()
	}
	org, err := c.client.ProjectOrganization(ctx, c.project)
	if err != nil {
		return nil, describeAccountError(err, c.project)
	}
	account, err := c.client.ProjectBillingAccount(ctx, org, c.project)
	if err != nil {
		return nil, describeAccountError(err, c.project)
	}
	if account == nil {
		return nil, errNoAccount
	}
	return &accountContext{caller: c, org: org, account: account}, ""
}

func (t *tools) getBillingAccount(ctx context.Context, _ *mcp.CallToolRequest, _ GetBillingAccountArgs) (*mcp.CallToolResult, any, error) {
	ac, msg := t.account(ctx)
	if msg != "" {
		return errorResult(msg), nil, nil
	}
	client := ac.caller.client

	var defaultPM *billingv1alpha1.PaymentMethod
	if ref := ac.account.Spec.DefaultPaymentMethodRef; ref != nil && ref.Name != "" {
		if methods, err := client.ListPaymentMethods(ctx, ac.org, ac.account.Name); err == nil {
			for i := range methods {
				if methods[i].Name == ref.Name {
					defaultPM = &methods[i]
				}
			}
		}
	}
	plan := client.AccountPlan(ctx, ac.org, ac.account.Name)
	return jsonResult(usagereport.SummarizeAccount(ac.account, plan, defaultPM, t.now()))
}

func (t *tools) listPaymentMethods(ctx context.Context, _ *mcp.CallToolRequest, _ ListPaymentMethodsArgs) (*mcp.CallToolResult, any, error) {
	ac, msg := t.account(ctx)
	if msg != "" {
		return errorResult(msg), nil, nil
	}
	methods, err := ac.caller.client.ListPaymentMethods(ctx, ac.org, ac.account.Name)
	if err != nil {
		return errorResult(describeAccountError(err, ac.caller.project)), nil, nil
	}
	defaultName := ""
	if ref := ac.account.Spec.DefaultPaymentMethodRef; ref != nil {
		defaultName = ref.Name
	}
	out := []usagereport.PaymentMethodSummary{}
	for _, pm := range methods {
		out = append(out, usagereport.SummarizePaymentMethod(pm, defaultName, t.now()))
	}
	return jsonResult(map[string]any{"billingAccount": ac.account.Name, "paymentMethods": out})
}

func (t *tools) listInvoices(ctx context.Context, _ *mcp.CallToolRequest, args ListInvoicesArgs) (*mcp.CallToolResult, any, error) {
	switch strings.ToLower(args.Status) {
	case "", "open", "paid", "pastdue", "void":
	default:
		return errorResult(fmt.Sprintf("Unknown status %q; use Open, Paid, PastDue or Void.", args.Status)), nil, nil
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultInvoiceLimit
	}
	limit = min(limit, maxInvoiceLimit)

	ac, msg := t.account(ctx)
	if msg != "" {
		return errorResult(msg), nil, nil
	}
	invoices, err := ac.caller.client.ListInvoices(ctx, ac.org, ac.account.Name)
	if err != nil {
		return errorResult(describeAccountError(err, ac.caller.project)), nil, nil
	}
	return jsonResult(map[string]any{
		"billingAccount": ac.account.Name,
		"invoices":       usagereport.SummarizeInvoices(invoices, args.Status, limit, t.now()),
	})
}

// describeAccountError explains a failure to read the organization's billing
// resources. They live in the organization, so a project-only member can't
// read them even when they can read the project's usage.
func describeAccountError(err error, project string) string {
	var apiErr *usagereport.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
		return fmt.Sprintf("The person asking can't see the billing account for project %s. Billing accounts, "+
			"payment methods and invoices belong to the organization, so this needs an organization role "+
			"(Viewer, Editor or Owner).", project)
	}
	return describeError(err, project)
}
