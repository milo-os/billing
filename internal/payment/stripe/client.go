// SPDX-License-Identifier: AGPL-3.0-only

package stripe

import (
	"context"
	"errors"
	"fmt"

	stripego "github.com/stripe/stripe-go/v81"
	stripeclient "github.com/stripe/stripe-go/v81/client"
)

// Client wraps the Stripe SDK and exposes only the operations the billing
// controllers need. A new Client should be constructed per reconcile from a
// ResolvedConfig — Stripe API keys are short-lived configuration, not a
// long-lived process-wide credential.
type Client struct {
	api *stripeclient.API
}

// NewClient builds a Stripe API client from a resolved config.
func NewClient(cfg *ResolvedConfig) *Client {
	api := stripeclient.New(cfg.SecretKey, nil)
	return &Client{api: api}
}

// EnsureCustomer creates a Stripe Customer for the given BillingAccount, or
// returns the existing customer id if one is already pinned via the
// supplied existingID. The returned id is the canonical `cus_…` identifier.
func (c *Client) EnsureCustomer(ctx context.Context, existingID, billingAccountName, email string) (string, error) {
	if existingID != "" {
		// Trust the recorded id; if it has been deleted upstream Stripe
		// returns a "resource_missing" error on subsequent operations
		// and the caller can decide how to handle it.
		return existingID, nil
	}
	params := &stripego.CustomerParams{
		Params: stripego.Params{Context: ctx},
		Metadata: map[string]string{
			"billing_account": billingAccountName,
		},
	}
	if email != "" {
		params.Email = stripego.String(email)
	}
	cu, err := c.api.Customers.New(params)
	if err != nil {
		return "", fmt.Errorf("creating Stripe customer: %w", err)
	}
	return cu.ID, nil
}

// CreateSetupIntent creates a SetupIntent for off-session future payments.
// The returned values are pushed onto PaymentMethodSetup.status.
func (c *Client) CreateSetupIntent(ctx context.Context, customerID, paymentMethodSetupName string) (id, clientSecret, status string, err error) {
	if customerID == "" {
		return "", "", "", errors.New("CreateSetupIntent requires a customer id")
	}
	params := &stripego.SetupIntentParams{
		Params:             stripego.Params{Context: ctx},
		Customer:           stripego.String(customerID),
		Usage:              stripego.String(string(stripego.SetupIntentUsageOffSession)),
		PaymentMethodTypes: stripego.StringSlice([]string{"card"}),
		Metadata: map[string]string{
			"payment_method_setup": paymentMethodSetupName,
		},
	}
	si, err := c.api.SetupIntents.New(params)
	if err != nil {
		return "", "", "", fmt.Errorf("creating Stripe SetupIntent: %w", err)
	}
	return si.ID, si.ClientSecret, string(si.Status), nil
}

// PaymentMethodDetails is the subset of Stripe PaymentMethod metadata the
// billing system records on BillingAccount.status.paymentMethod.
type PaymentMethodDetails struct {
	ID        string
	Brand     string
	Last4     string
	BIN       string // First 6+ digits when available; otherwise empty.
	Country   string
	ExpMonth  int32
	ExpYear   int32
	AVSResult string // From the most recent SetupIntent charge attempt.
	CVCResult string
}

// RetrievePaymentMethod fetches a PaymentMethod by id and returns its
// public-safe details.
func (c *Client) RetrievePaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetails, error) {
	pm, err := c.api.PaymentMethods.Get(paymentMethodID, &stripego.PaymentMethodParams{
		Params: stripego.Params{Context: ctx},
	})
	if err != nil {
		return nil, fmt.Errorf("retrieving PaymentMethod %q: %w", paymentMethodID, err)
	}
	out := &PaymentMethodDetails{ID: pm.ID}
	if pm.Card != nil {
		out.Brand = string(pm.Card.Brand)
		out.Last4 = pm.Card.Last4
		out.Country = pm.Card.Country
		out.ExpMonth = int32(pm.Card.ExpMonth)
		out.ExpYear = int32(pm.Card.ExpYear)
		if pm.Card.Checks != nil {
			out.AVSResult = stringOr(string(pm.Card.Checks.AddressLine1Check), string(pm.Card.Checks.AddressPostalCodeCheck))
			out.CVCResult = string(pm.Card.Checks.CVCCheck)
		}
		// Stripe does not expose BIN by default; if the account has the
		// `card_iin` feature enabled, the field will be present here.
		// Best-effort population from the IIN/Fingerprint helpers.
		if pm.Card.IIN != "" {
			out.BIN = pm.Card.IIN
		}
	}
	return out, nil
}

func stringOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
