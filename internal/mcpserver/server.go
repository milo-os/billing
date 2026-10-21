// SPDX-License-Identifier: AGPL-3.0-only

// Package mcpserver serves billing's tools to Datum's assistant (Patch) over
// MCP, alongside the documents it reads before calling them:
//
//	POST /mcp                  Streamable HTTP MCP, stateless
//	GET  /llms-full.txt        Knowledge: usage, meters and spend
//	GET  /runbooks/<name>.md   Skills: step-by-step procedures
//	GET  /healthz              Liveness
//
// The server holds no credential of its own. Each request is served by a
// fresh MCP server whose tools read through Milo with the caller's bearer
// token, in the project named by the X-Datum-Project header. A tool call
// never sees more than the person who asked, and Milo and the usage API
// remain the only enforcement points.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	// MCPPath serves the Streamable HTTP MCP endpoint.
	MCPPath = "/mcp"
	// HealthzPath serves liveness.
	HealthzPath = "/healthz"

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// The response streams while a tool runs, so the write timeout has to
	// outlast the slowest tool call.
	writeTimeout    = ToolCallTimeout + 10*time.Second
	idleTimeout     = 120 * time.Second
	shutdownTimeout = 25 * time.Second // inside the default 30s termination grace period

	// maxRequestBytes caps an /mcp request body. Tool arguments are a few
	// short strings.
	maxRequestBytes = 1 << 20
)

// Options configures the handler.
type Options struct {
	// BaseConfig supplies the Milo API endpoint and CA. Any credentials it
	// carries are ignored. It must pass CheckBaseConfig.
	BaseConfig *rest.Config
	// Name and Version identify the server to MCP clients.
	Name    string
	Version string
	// Now is the clock tools read the current billing cycle from; it
	// defaults to time.Now.
	Now func() time.Time
}

// NewHandler returns the mux serving every route.
func NewHandler(opts Options) (http.Handler, error) {
	if err := CheckBaseConfig(opts.BaseConfig); err != nil {
		return nil, err
	}
	if opts.Name == "" {
		opts.Name = "billing"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	docs, err := newDocsHandler()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(MCPPath, http.MaxBytesHandler(newMCPHandler(opts), maxRequestBytes))
	mux.Handle(knowledgePath, docs)
	mux.Handle(runbookPrefix, docs)
	mux.HandleFunc(HealthzPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux, nil
}

// newMCPHandler returns the stateless Streamable HTTP handler. Every request
// gets its own MCP server bound to that caller, so nothing is shared between
// callers.
func newMCPHandler(opts Options) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			s := mcp.NewServer(&mcp.Implementation{Name: opts.Name, Version: opts.Version}, nil)
			s.AddReceivingMiddleware(recoverMiddleware)
			registerTools(s, &tools{resolve: resolverFromRequest(r, opts.BaseConfig), now: opts.Now})
			return s
		},
		// The tools are read-only and need no session; stateless also keeps
		// the server robust against client restarts and replica changes.
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}

// Serve listens on addr and serves handler until ctx is cancelled, then
// drains in-flight requests.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		// Requests keep ctx's values but not its cancellation: on SIGTERM,
		// Shutdown stops accepting and lets in-flight tool calls finish.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	errCh := make(chan error, 1)
	go func() {
		klog.InfoS("Serving billing MCP over HTTP", "addr", ln.Addr().String(), "mcp", MCPPath, "tools", Tools)
		errCh <- server.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving on %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
