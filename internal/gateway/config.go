// SPDX-License-Identifier: AGPL-3.0-only

// Package gateway implements the usage event ingestion gateway.
package gateway

// Config is the resolved runtime configuration for the Gateway.
type Config struct {
	// Addr is the HTTP listen address for ingest endpoints (default :8080).
	Addr string
	// NATSUrl is the NATS JetStream URL (required).
	NATSUrl string
	// NATSSubjectPrefix is the NATS subject prefix (default "billing.usage").
	NATSSubjectPrefix string
	// Audience is the expected TokenReview audience (default "billing-gateway").
	Audience string
	// HealthAddr is the health/readiness probe address (default :8081).
	HealthAddr string
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
