// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.datum.net/datumctl/plugin"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/usagereport"
)

// api is what the commands need from Datum Cloud; tests swap in a fake.
type api interface {
	queryUsage(ctx context.Context, s usagereport.Scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error)
	listMeterDefinitions(ctx context.Context) ([]billingv1alpha1.MeterDefinition, error)
	// paymentTerms returns the billing account's terms for the scope, or nil
	// when they can't be read; callers fall back to calendar months.
	paymentTerms(ctx context.Context, s usagereport.Scope) *billingv1alpha1.PaymentTerms
}

// datumClient reads through Milo as the datumctl session that runs the
// plugin, turning API errors into advice for the person at the terminal.
type datumClient struct {
	client *usagereport.Client
}

// newDatumClient builds a client from the context datumctl injects into a
// plugin it runs. The plugin never stores a credential: it asks datumctl's
// credentials helper for a fresh token before every request.
func newDatumClient() (api, error) {
	ctx := plugin.Context()
	if ctx.APIHost == "" || ctx.CredentialsHelper == "" {
		return nil, fmt.Errorf("no Datum Cloud session found; run this as `datumctl billing`")
	}
	return &datumClient{client: &usagereport.Client{
		BaseURL: strings.TrimRight(ensureScheme(ctx.APIHost), "/"),
		HTTP:    &http.Client{Timeout: 90 * time.Second},
		Token:   plugin.Token,
	}}, nil
}

// ensureScheme prepends https:// when datumctl provides a bare host.
func ensureScheme(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

func (c *datumClient) queryUsage(ctx context.Context, s usagereport.Scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error) {
	q, err := c.client.QueryUsage(ctx, s, spec)
	if err != nil {
		return nil, describeError(err, s)
	}
	return q, nil
}

func (c *datumClient) listMeterDefinitions(ctx context.Context) ([]billingv1alpha1.MeterDefinition, error) {
	return c.client.ListMeterDefinitions(ctx)
}

func (c *datumClient) paymentTerms(ctx context.Context, s usagereport.Scope) *billingv1alpha1.PaymentTerms {
	return c.client.PaymentTerms(ctx, s)
}

// describeError turns the UsageQuery errors a user can act on into advice.
func describeError(err error, s usagereport.Scope) error {
	apiErr, ok := err.(*usagereport.APIError)
	if !ok {
		return err
	}
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("your session has expired; run `datumctl login`")
	case http.StatusForbidden:
		return fmt.Errorf("you don't have access to usage in %s: %s", s, apiErr.Message)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("the usage service is unavailable; try again shortly")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return usageErrorf("%s", apiErr.Message)
	}
	return err
}
