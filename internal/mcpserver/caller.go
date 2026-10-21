// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"

	"go.miloapis.com/billing/internal/usagereport"
)

const (
	// ProjectHeader names the project a request reads. It is set by the
	// already-authenticated client calling this server (Patch), never by the
	// model: a model that could name its own project would be one prompt
	// injection away from another tenant's usage.
	ProjectHeader = "X-Datum-Project"

	// misconfiguredClientNote closes every error a request can fail with
	// before a read: all of them are the calling client's configuration, so
	// the assistant shouldn't advise the person to sign in again.
	misconfiguredClientNote = "The person who asked did nothing wrong and re-authenticating will not " +
		"help: this is a configuration problem for whoever operates that client"
)

// caller is one request's identity: the person who asked, in the project the
// conversation is about.
type caller struct {
	client  *usagereport.Client
	project string
}

// resolver returns the request's caller, built once on first use.
type resolver func(context.Context) (*caller, error)

// resolverFromRequest reads the caller's bearer token and project from r.
// Nothing is checked until a tool needs it, so an MCP handshake (initialize,
// tools/list) works without them.
func resolverFromRequest(r *http.Request, base *rest.Config) resolver {
	token := bearerToken(r)
	project := strings.TrimSpace(r.Header.Get(ProjectHeader))

	var (
		once sync.Once
		c    *caller
		err  error
	)
	return func(context.Context) (*caller, error) {
		once.Do(func() { c, err = newCaller(base, token, project) })
		return c, err
	}
}

func newCaller(base *rest.Config, token, project string) (*caller, error) {
	if token == "" {
		return nil, fmt.Errorf("no credentials on this request: the client that called this tool did not "+
			"forward the user's identity. %s", misconfiguredClientNote)
	}
	if project == "" {
		return nil, fmt.Errorf("no project on this request: the client that called this tool did not set "+
			"the %s header. %s", ProjectHeader, misconfiguredClientNote)
	}
	// The project ends up in a URL path, so it must be a plain name.
	if errs := validation.IsDNS1123Label(project); len(errs) > 0 {
		return nil, fmt.Errorf("invalid project %q on the %s header: %s. %s",
			project, ProjectHeader, strings.Join(errs, "; "), misconfiguredClientNote)
	}

	// Only the endpoint and CA come from the base config. Every credential
	// it might carry is dropped, so this server's own identity can never
	// reach a caller's read.
	httpClient, err := rest.HTTPClientFor(rest.AnonymousClientConfig(base))
	if err != nil {
		return nil, fmt.Errorf("building HTTP client: %w", err)
	}
	return &caller{
		project: project,
		client: &usagereport.Client{
			BaseURL: strings.TrimRight(withScheme(base.Host), "/"),
			HTTP:    httpClient,
			Token:   func() (string, error) { return token, nil },
		},
	}, nil
}

func bearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func withScheme(host string) string {
	if strings.Contains(host, "://") {
		return host
	}
	return "https://" + host
}

// CheckBaseConfig refuses a base configuration this server can't use
// safely, so a deployment mistake fails at boot rather than as a stream of
// tool errors that read like the caller's fault.
func CheckBaseConfig(cfg *rest.Config) error {
	if cfg == nil || cfg.Host == "" {
		return fmt.Errorf("control plane endpoint is empty: the kubeconfig must name the Milo API server")
	}
	u, err := url.Parse(withScheme(cfg.Host))
	if err != nil {
		return fmt.Errorf("parsing control plane host %q: %w", cfg.Host, err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("control plane endpoint %s must be a bare address: requests read only with "+
			"each caller's own token", cfg.Host)
	}
	if p := strings.TrimSuffix(u.Path, "/"); p != "" {
		return fmt.Errorf("control plane endpoint %s already has a path (%q): requests append each "+
			"caller's control-plane path themselves", cfg.Host, u.Path)
	}
	// This server reads Datum control planes through Milo, never the cluster
	// it runs in, so an in-cluster fallback is always a mistake.
	if host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"); host != "" && port != "" {
		if strings.TrimSuffix(cfg.Host, "/") == "https://"+host+":"+port {
			return fmt.Errorf("control plane endpoint %s is this cluster's own API server; mount a "+
				"kubeconfig naming the Milo API server", cfg.Host)
		}
	}
	return nil
}
