// SPDX-License-Identifier: AGPL-3.0-only

// Package gateway implements the usage event ingestion gateway.
package gateway

// Config is the resolved runtime configuration for the Gateway.
type Config struct {
	// Addr is the HTTP listen address for ingest endpoints (default :8080).
	Addr string
	// HealthProbeAddr is the address the manager binds for health/readiness
	// probes (default :8081). Set to "" to disable.
	HealthProbeAddr string
	// MetricsAddr is the address the manager binds for Prometheus metrics
	// (default :8082). Set to "0" to disable.
	MetricsAddr string
	// NATSUrl is the NATS JetStream URL (required).
	NATSUrl string
	// NATSSubjectPrefix is the NATS subject prefix (default "billing.usage").
	NATSSubjectPrefix string
	// EnableGatewayAttributionCheck is a temporary safeguard: when true the
	// gateway watches Milo and drops usage for projects with no billable
	// account before NATS. When false the gateway does no Milo watches and
	// publishes every structurally valid event. KubeconfigPath is required
	// when this is true.
	EnableGatewayAttributionCheck bool
	// KubeconfigPath is the path to a kubeconfig for the Milo API server.
	// Used only when EnableGatewayAttributionCheck is true.
	KubeconfigPath string
	// TLSCertFile is the path to the TLS certificate file (required for HTTPS).
	TLSCertFile string
	// TLSKeyFile is the path to the TLS private key file (required for HTTPS).
	TLSKeyFile string
	// NATSCAFile is the path to the NATS CA certificate file.
	NATSCAFile string
	// NATSCertFile is the path to the NATS client certificate file.
	NATSCertFile string
	// NATSKeyFile is the path to the NATS client private key file.
	NATSKeyFile string
}
