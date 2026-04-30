// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/client-go/kubernetes"

	"go.miloapis.com/billing/internal/gateway/auth"
	"go.miloapis.com/billing/internal/gateway/handler"
	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

var serverLog = ctrl.Log.WithName("gateway")

// Run is the entry point called by cmd/billing/cmd/gateway.go.
// It assembles all dependencies and starts the HTTP servers.
func Run(ctx context.Context, cfg Config) error {
	// 1. Build Kubernetes client.
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("creating Kubernetes client: %w", err)
	}

	// 2. Build TokenVerifier.
	verifier := auth.NewServiceAccountTokenVerifier(k8sClient, cfg.Audience)

	// 3. Build NATSPublisher (fatal on error).
	publisher, err := gwnats.NewNATSPublisher(cfg.NATSUrl)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}

	// 4. Build OTel metrics with Prometheus exporter.
	promExporter, err := prometheusexporter.New()
	if err != nil {
		return fmt.Errorf("creating Prometheus exporter: %w", err)
	}
	mp := metric.NewMeterProvider(metric.WithReader(promExporter))
	defer func() { _ = mp.Shutdown(context.Background()) }()

	metrics, err := newGatewayMetrics(mp)
	if err != nil {
		return fmt.Errorf("registering metrics: %w", err)
	}

	// 5. Build ingest mux (TLS + auth middleware).
	ingestMux := http.NewServeMux()
	ingestMux.Handle("POST /v1/usage/events",
		handler.AuthMiddleware(verifier, handler.NewIngestHandler(publisher, metrics, cfg.NATSSubjectPrefix)))
	ingestMux.Handle("POST /v1/usage/events:batchIngest",
		handler.AuthMiddleware(verifier, handler.NewBatchIngestHandler(publisher, metrics, cfg.NATSSubjectPrefix)))

	// 6. Build health/metrics mux (no TLS, no auth).
	healthMux := http.NewServeMux()
	healthMux.Handle("GET /healthz", handler.NewHealthHandler())
	healthMux.Handle("GET /readyz", handler.NewReadyHandler(publisher))
	healthMux.Handle("GET /metrics", promhttp.Handler())

	// 7. Load TLS config for ingest server.
	tlsCert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("loading TLS certificate: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}

	ingestServer := &http.Server{
		Addr:      cfg.Addr,
		Handler:   ingestMux,
		TLSConfig: tlsCfg,
	}
	healthServer := &http.Server{
		Addr:    cfg.HealthAddr,
		Handler: healthMux,
	}

	// 8. Start servers and block until context is cancelled or a server fails.
	errCh := make(chan error, 2)

	go func() {
		serverLog.Info("starting ingest server (TLS)", "addr", cfg.Addr)
		if err := ingestServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("ingest server: %w", err)
		}
	}()

	go func() {
		serverLog.Info("starting health server", "addr", cfg.HealthAddr)
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("health server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		serverLog.Info("shutting down gateway servers")
		_ = ingestServer.Shutdown(context.Background())
		_ = healthServer.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		return err
	}
}
