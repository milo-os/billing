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
	serverLog.Info("starting billing gateway",
		"addr", cfg.Addr,
		"healthAddr", cfg.HealthAddr,
		"natsURL", cfg.NATSUrl,
		"natsSubjectPrefix", cfg.NATSSubjectPrefix,
		"audience", cfg.Audience,
	)

	// 1. Build Kubernetes client.
	serverLog.Info("loading kubeconfig")
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		serverLog.Error(err, "failed to load kubeconfig")
		return fmt.Errorf("loading kubeconfig: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		serverLog.Error(err, "failed to create Kubernetes client")
		return fmt.Errorf("creating Kubernetes client: %w", err)
	}
	serverLog.Info("Kubernetes client ready")

	// 2. Build TokenVerifier.
	verifier := auth.NewServiceAccountTokenVerifier(k8sClient, cfg.Audience)
	serverLog.Info("token verifier ready", "audience", cfg.Audience)

	// 3. Build NATSPublisher (fatal on error).
	serverLog.Info("connecting to NATS", "url", cfg.NATSUrl)
	publisher, err := gwnats.NewNATSPublisher(cfg.NATSUrl)
	if err != nil {
		serverLog.Error(err, "failed to connect to NATS", "url", cfg.NATSUrl)
		return fmt.Errorf("connecting to NATS: %w", err)
	}

	// 4. Build OTel metrics with Prometheus exporter.
	serverLog.Info("initializing metrics")
	promExporter, err := prometheusexporter.New()
	if err != nil {
		serverLog.Error(err, "failed to create Prometheus exporter")
		return fmt.Errorf("creating Prometheus exporter: %w", err)
	}
	mp := metric.NewMeterProvider(metric.WithReader(promExporter))
	defer func() { _ = mp.Shutdown(context.Background()) }()

	metrics, err := newGatewayMetrics(mp)
	if err != nil {
		serverLog.Error(err, "failed to register metrics")
		return fmt.Errorf("registering metrics: %w", err)
	}
	serverLog.Info("metrics ready")

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
	serverLog.Info("loading TLS certificate", "certFile", cfg.TLSCertFile)
	tlsCert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		serverLog.Error(err, "failed to load TLS certificate", "certFile", cfg.TLSCertFile, "keyFile", cfg.TLSKeyFile)
		return fmt.Errorf("loading TLS certificate: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}
	serverLog.Info("TLS certificate loaded")

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
			serverLog.Error(err, "ingest server stopped unexpectedly")
			errCh <- fmt.Errorf("ingest server: %w", err)
		}
	}()

	go func() {
		serverLog.Info("starting health server", "addr", cfg.HealthAddr)
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverLog.Error(err, "health server stopped unexpectedly")
			errCh <- fmt.Errorf("health server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		serverLog.Info("shutting down gateway servers")
		_ = ingestServer.Shutdown(context.Background())
		_ = healthServer.Shutdown(context.Background())
		serverLog.Info("gateway stopped")
		return nil
	case err := <-errCh:
		return err
	}
}
