// SPDX-License-Identifier: AGPL-3.0-only

package stripe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// ServerOptions configures the standalone HTTP listener that receives
// Stripe webhooks. The webhook listens on its own port (rather than
// piggy-backing on the metrics/probe server) so the public ingress can
// expose only this single endpoint.
type ServerOptions struct {
	// Addr is the listen address (e.g. ":8443" or ":8090").
	Addr string

	// ProviderName is the PaymentProvider this server authenticates
	// against. Today only one Stripe provider is expected per cluster.
	ProviderName string

	// TLSCertFile and TLSKeyFile, if both set, enable HTTPS termination
	// in-process. In typical deployments TLS is terminated upstream and
	// these are left empty.
	TLSCertFile string
	TLSKeyFile  string
}

// NewRunnable constructs a manager.Runnable that serves the Stripe webhook
// using the manager's client (so it shares the same cache and credentials).
func NewRunnable(opts ServerOptions, mgr manager.Manager) (manager.Runnable, error) {
	if opts.Addr == "" {
		return nil, errors.New("stripe webhook server: Addr is required")
	}
	if opts.ProviderName == "" {
		return nil, errors.New("stripe webhook server: ProviderName is required")
	}

	handler := &WebhookHandler{
		Client:       mgr.GetClient(),
		ProviderName: opts.ProviderName,
		SeenEvents:   NewMemoryDeduper(0),
	}

	mux := http.NewServeMux()
	mux.Handle(WebhookPath, handler)

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if opts.TLSCertFile != "" && opts.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.TLSCertFile, opts.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading TLS cert for stripe webhook: %w", err)
		}
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	return &runnable{srv: srv}, nil
}

type runnable struct {
	srv *http.Server
}

func (r *runnable) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("stripe-webhook")
	errCh := make(chan error, 1)

	go func() {
		log.Info("starting stripe webhook server", "addr", r.srv.Addr, "tls", r.srv.TLSConfig != nil)
		var err error
		if r.srv.TLSConfig != nil {
			err = r.srv.ListenAndServeTLS("", "")
		} else {
			err = r.srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}
