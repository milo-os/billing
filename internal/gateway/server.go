// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"go.miloapis.com/billing/internal/gateway/handler"
	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

var serverLog = ctrl.Log.WithName("gateway")

// Run is the entry point called by cmd/billing/cmd/gateway.go.
// It assembles all dependencies and starts the HTTP servers.
func Run(ctx context.Context, cfg Config) error {
	serverLog.Info("starting billing gateway",
		"addr", cfg.Addr,
		"healthProbeAddr", cfg.HealthProbeAddr,
		"metricsAddr", cfg.MetricsAddr,
		"natsURL", cfg.NATSUrl,
		"natsSubjectPrefix", cfg.NATSSubjectPrefix,
	)

	// 1. Build NATSPublisher (fatal on error).
	serverLog.Info("connecting to NATS", "url", cfg.NATSUrl)
	publisher, err := gwnats.NewNATSPublisher(cfg.NATSUrl, cfg.NATSCAFile, cfg.NATSCertFile, cfg.NATSKeyFile)
	if err != nil {
		serverLog.Error(err, "failed to connect to NATS", "url", cfg.NATSUrl)
		return fmt.Errorf("connecting to NATS: %w", err)
	}

	// 2. Build OTel metrics with Prometheus exporter.
	// prometheusexporter.New() registers with prometheus.DefaultRegisterer, which
	// controller-runtime's metrics server exposes via promhttp.Handler().
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

	// 3. Build the controller-runtime manager for health probes and metrics.
	// The manager owns /healthz, /readyz, and /metrics — the gateway does not
	// run controllers or a cache, so we disable leader election.
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		serverLog.Error(err, "failed to load kubeconfig")
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		HealthProbeBindAddress: cfg.HealthProbeAddr,
		Metrics: metricsserver.Options{
			BindAddress: cfg.MetricsAddr,
		},
		LeaderElection: false,
	})
	if err != nil {
		serverLog.Error(err, "failed to create manager")
		return fmt.Errorf("creating manager: %w", err)
	}

	// 4. Register health checks with the manager.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("registering healthz check: %w", err)
	}
	// The gateway is only ready when the NATS upstream is connected.
	if err := mgr.AddReadyzCheck("nats", func(_ *http.Request) error {
		if !publisher.Healthy() {
			return errors.New("NATS connection is not healthy")
		}
		return nil
	}); err != nil {
		return fmt.Errorf("registering nats readyz check: %w", err)
	}

	// 5. Build ingest mux. Authentication is handled by the upstream Envoy
	// gateway; no auth middleware is applied here.
	ingestMux := http.NewServeMux()
	ingestMux.Handle("POST /v1/usage/events",
		handler.NewIngestHandler(publisher, metrics, cfg.NATSSubjectPrefix))
	ingestMux.Handle("POST /v1/usage/events:batchIngest",
		handler.NewBatchIngestHandler(publisher, metrics, cfg.NATSSubjectPrefix))

	// 6. Optionally load TLS for the ingest server.
	var ingestServer *http.Server
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		serverLog.Info("loading TLS certificate", "certFile", cfg.TLSCertFile)
		tlsCert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			serverLog.Error(err, "failed to load TLS certificate",
				"certFile", cfg.TLSCertFile, "keyFile", cfg.TLSKeyFile)
			return fmt.Errorf("loading TLS certificate: %w", err)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		}
		serverLog.Info("TLS certificate loaded")
		ingestServer = &http.Server{
			Addr:      cfg.Addr,
			Handler:   ingestMux,
			TLSConfig: tlsCfg,
		}
	} else {
		serverLog.Info("no TLS certificate provided; starting ingest server (HTTP)")
		ingestServer = &http.Server{
			Addr:    cfg.Addr,
			Handler: ingestMux,
		}
	}

	// 7. Start the manager and ingest server concurrently.
	// The manager owns health/metrics; the ingest server handles usage events.
	// Both are shut down when ctx is cancelled or either returns an error.
	mgrCtx, cancelMgr := context.WithCancel(ctx)
	defer cancelMgr()

	errCh := make(chan error, 2)

	go func() {
		serverLog.Info("starting manager (health/metrics)")
		if err := mgr.Start(mgrCtx); err != nil {
			serverLog.Error(err, "manager stopped unexpectedly")
			errCh <- fmt.Errorf("manager: %w", err)
		}
	}()

	go func() {
		if ingestServer.TLSConfig != nil {
			serverLog.Info("starting ingest server (TLS)", "addr", cfg.Addr)
			if err := ingestServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverLog.Error(err, "ingest server stopped unexpectedly")
				errCh <- fmt.Errorf("ingest server: %w", err)
			}
		} else {
			serverLog.Info("starting ingest server (HTTP)", "addr", cfg.Addr)
			if err := ingestServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverLog.Error(err, "ingest server stopped unexpectedly")
				errCh <- fmt.Errorf("ingest server: %w", err)
			}
		}
	}()

	select {
	case <-ctx.Done():
		serverLog.Info("shutting down gateway")
		_ = ingestServer.Shutdown(context.Background())
		cancelMgr()
		serverLog.Info("gateway stopped")
		return nil
	case err := <-errCh:
		return err
	}
}
