// SPDX-License-Identifier: AGPL-3.0-only

package usagereport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	usagev1alpha1 "go.miloapis.com/billing/api/usage/v1alpha1"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// Scope is where a request is made. A project query is made in the
// project's own context and only sees that project's usage.
type Scope struct {
	Org     string
	Project string
}

// ControlPlane is the Milo path for the scope's control plane.
func (s Scope) ControlPlane() string {
	if s.Project != "" {
		return "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + s.Project + "/control-plane"
	}
	return "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/" + s.Org + "/control-plane"
}

func (s Scope) String() string {
	if s.Project != "" {
		return "project " + s.Project
	}
	return "organization " + s.Org
}

// Client reads usage through Milo as one caller. It holds no credential of
// its own: Token returns the caller's, fresh for every request.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   func() (string, error)
}

// QueryUsage makes a UsageQuery in the scope's context.
func (c *Client) QueryUsage(ctx context.Context, s Scope, spec usagev1alpha1.UsageQuerySpec) (*usagev1alpha1.UsageQuery, error) {
	body := usagev1alpha1.UsageQuery{Spec: spec}
	body.APIVersion = usagev1alpha1.GroupVersion.String()
	body.Kind = "UsageQuery"
	var out usagev1alpha1.UsageQuery
	path := s.ControlPlane() + "/apis/usage.billing.miloapis.com/v1alpha1/usagequeries"
	if err := c.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMeterDefinitions lists the platform's meter catalog.
func (c *Client) ListMeterDefinitions(ctx context.Context) ([]billingv1alpha1.MeterDefinition, error) {
	var out billingv1alpha1.MeterDefinitionList
	if err := c.Do(ctx, http.MethodGet, "/apis/billing.miloapis.com/v1alpha1/meterdefinitions", nil, &out); err != nil {
		return nil, fmt.Errorf("listing meter definitions: %w", err)
	}
	return out.Items, nil
}

// ProjectOrganization returns the organization a project belongs to.
func (c *Client) ProjectOrganization(ctx context.Context, project string) (string, error) {
	var out struct {
		Spec struct {
			OwnerRef struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerRef"`
		} `json:"spec"`
	}
	if err := c.Do(ctx, http.MethodGet, "/apis/resourcemanager.miloapis.com/v1alpha1/projects/"+project, nil, &out); err != nil {
		return "", err
	}
	if out.Spec.OwnerRef.Kind != "Organization" || out.Spec.OwnerRef.Name == "" {
		return "", fmt.Errorf("project %s has no owning organization", project)
	}
	return out.Spec.OwnerRef.Name, nil
}

// PaymentTerms returns the billing account's terms for the scope, or nil
// when they can't be read (callers fall back to calendar months). A project
// uses its bound account; an organization its first Ready account, or its
// first account when none are Ready yet.
func (c *Client) PaymentTerms(ctx context.Context, s Scope) *billingv1alpha1.PaymentTerms {
	if s.Org == "" {
		return nil
	}
	base := Scope{Org: s.Org}.ControlPlane() + "/apis/billing.miloapis.com/v1alpha1/namespaces/organization-" + s.Org

	accountName := ""
	if s.Project != "" {
		var bindings billingv1alpha1.BillingAccountBindingList
		if err := c.Do(ctx, http.MethodGet, base+"/billingaccountbindings", nil, &bindings); err != nil {
			return nil
		}
		for _, b := range bindings.Items {
			if b.Spec.ProjectRef.Name == s.Project &&
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
	if err := c.Do(ctx, http.MethodGet, base+"/billingaccounts", nil, &accounts); err != nil {
		return nil
	}
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

// APIError is a non-2xx response, with the server's message when it sent a
// Kubernetes Status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// Do sends one request as the caller and decodes a JSON response into out.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	token, err := c.Token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
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
		return &APIError{Status: resp.StatusCode, Message: status.Message}
	}
	return json.Unmarshal(data, out)
}
