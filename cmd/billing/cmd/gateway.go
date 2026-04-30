// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"flag"
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

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

	opts := zap.Options{
		Development: true,
	}

	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the usage event ingestion gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

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

	zapFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	opts.BindFlags(zapFlags)
	cmd.Flags().AddGoFlagSet(zapFlags)

	return cmd
}
