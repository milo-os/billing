package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.datum.net/datumctl/plugin"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// scope is where a request is made. Like the portal, a project query is made
// in the project's own context and only sees that project's usage.
type scope struct {
	org     string
	project string
}

func (s scope) controlPlane() string {
	if s.project != "" {
		return "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + s.project + "/control-plane"
	}
	return "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/" + s.org + "/control-plane"
}

func (s scope) String() string {
	if s.project != "" {
		return "project " + s.project
	}
	return "organization " + s.org
}

// api is what the commands need from Datum Cloud; tests swap in a fake.
type api interface {
	queryUsage(ctx context.Context, s scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error)
	listMeterDefinitions(ctx context.Context) ([]billingv1alpha1.MeterDefinition, error)
	// paymentTerms returns the billing account's terms for the scope, or nil
	// when they can't be read; callers fall back to calendar months.
	paymentTerms(ctx context.Context, s scope) *billingv1alpha1.PaymentTerms
}

type datumClient struct {
	base  string
	token func() (string, error)
	http  *http.Client
}

// newDatumClient builds a client from the context datumctl injects into a
// plugin it runs. The plugin never stores a credential: it asks datumctl's
// credentials helper for a fresh token before every request.
func newDatumClient() (api, error) {
	ctx := plugin.Context()
	if ctx.APIHost == "" || ctx.CredentialsHelper == "" {
		return nil, fmt.Errorf("no Datum Cloud session found; run this as `datumctl billing`")
	}
	return &datumClient{
		base:  strings.TrimRight(ensureScheme(ctx.APIHost), "/"),
		token: plugin.Token,
		http:  &http.Client{Timeout: 90 * time.Second},
	}, nil
}

// ensureScheme prepends https:// when datumctl provides a bare host.
func ensureScheme(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

func (c *datumClient) queryUsage(ctx context.Context, s scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error) {
	body := usagev1alpha1.UsageQuery{Spec: spec}
	body.APIVersion = usagev1alpha1.GroupVersion.String()
	body.Kind = "UsageQuery"
	var out usagev1alpha1.UsageQuery
	path := s.controlPlane() + "/apis/usage.billing.miloapis.com/v1alpha1/usagequeries"
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, describeError(err, s)
	}
	return &out, nil
}

func (c *datumClient) listMeterDefinitions(ctx context.Context) ([]billingv1alpha1.MeterDefinition, error) {
	var out billingv1alpha1.MeterDefinitionList
	if err := c.do(ctx, http.MethodGet, "/apis/billing.miloapis.com/v1alpha1/meterdefinitions", nil, &out); err != nil {
		return nil, fmt.Errorf("listing meter definitions: %w", err)
	}
	return out.Items, nil
}

func (c *datumClient) paymentTerms(ctx context.Context, s scope) *billingv1alpha1.PaymentTerms {
	if s.org == "" {
		return nil
	}
	base := scope{org: s.org}.controlPlane() + "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-" + s.org

	accountName := ""
	if s.project != "" {
		var bindings billingv1alpha1.BillingAccountBindingList
		if err := c.do(ctx, http.MethodGet, base+"/billingaccountbindings", nil, &bindings); err != nil {
			return nil
		}
		for _, b := range bindings.Items {
			if b.Spec.ProjectRef.Name == s.project &&
				(b.Status.Phase == "" || b.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive) {
				accountName = b.Spec.BillingAccountRef.Name
				break
			}
		}
		if accountName == "" {
			return nil
		}
	}

	var accounts billingv1alpha1.BillingAccountList
	if err := c.do(ctx, http.MethodGet, base+"/billingaccounts", nil, &accounts); err != nil {
		return nil
	}
	// The cycle picker uses the bound account in a project, otherwise the
	// first Ready account (or the first account when none are Ready yet).
	var chosen *billingv1alpha1.BillingAccount
	for i := range accounts.Items {
		a := &accounts.Items[i]
		switch {
		case accountName != "":
			if a.Name == accountName {
				chosen = a
			}
		case a.Status.Phase == billingv1alpha1.BillingAccountPhaseReady && chosen == nil:
			chosen = a
		}
	}
	if chosen == nil && accountName == "" && len(accounts.Items) > 0 {
		chosen = &accounts.Items[0]
	}
	if chosen == nil {
		return nil
	}
	return chosen.Spec.PaymentTerms
}

// apiError is a non-2xx response, with the server's message when it sent a
// Kubernetes Status.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.message, e.status)
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

func (c *datumClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var status struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &status)
		return &apiError{status: resp.StatusCode, message: status.Message}
	}
	return json.Unmarshal(data, out)
}

// describeError turns the UsageQuery errors a user can act on into advice.
func describeError(err error, s scope) error {
	apiErr, ok := err.(*apiError)
	if !ok {
		return err
	}
	switch apiErr.status {
	case http.StatusUnauthorized:
		return fmt.Errorf("your session has expired; run `datumctl login`")
	case http.StatusForbidden:
		return fmt.Errorf("you don't have access to usage in %s: %s", s, apiErr.message)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("the usage service is unavailable; try again shortly")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return usageErrorf("%s", apiErr.message)
	}
	return err
}
