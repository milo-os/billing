// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.miloapis.com/billing/internal/gateway"
)

func newGatewayCommand() *cobra.Command {
	var (
		addr              string
		natsURL           string
		natsSubjectPrefix string
		audience          string
		healthAddr        string
		tlsCertFile       string
		tlsKeyFile        string
	)

	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the usage event ingestion gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tlsCertFile == "" || tlsKeyFile == "" {
				return fmt.Errorf("--tls-cert-file and --tls-key-file are required")
			}
			return gateway.Run(cmd.Context(), gateway.Config{
				Addr:              addr,
				NATSUrl:           natsURL,
				NATSSubjectPrefix: natsSubjectPrefix,
				Audience:          audience,
				HealthAddr:        healthAddr,
				TLSCertFile:       tlsCertFile,
				TLSKeyFile:        tlsKeyFile,
			})
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address for ingest endpoints.")
	cmd.Flags().StringVar(&natsURL, "nats-url", "", "NATS JetStream URL (required).")
	cmd.Flags().StringVar(&natsSubjectPrefix, "nats-subject-prefix", "billing.usage", "NATS subject prefix.")
	cmd.Flags().StringVar(&audience, "token-review-audience", "billing-gateway", "Expected TokenReview audience.")
	cmd.Flags().StringVar(&healthAddr, "health-probe-bind-address", ":8081", "Health/readiness probe address.")
	cmd.Flags().StringVar(&tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file (required).")
	cmd.Flags().StringVar(&tlsKeyFile, "tls-key-file", "", "Path to TLS private key file (required).")
	_ = cmd.MarkFlagRequired("nats-url")

	return cmd
}
