// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"flag"

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
		healthProbeAddr   string
		metricsAddr       string
		tlsCertFile       string
		tlsKeyFile        string
		natsCAFile        string
		natsCertFile      string
		natsKeyFile       string
		kubeconfigPath    string
	)

	opts := zap.Options{
		Development: true,
	}

	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run the usage event ingestion gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

			return gateway.Run(cmd.Context(), gateway.Config{
				Addr:              addr,
				NATSUrl:           natsURL,
				NATSSubjectPrefix: natsSubjectPrefix,
				HealthProbeAddr:   healthProbeAddr,
				MetricsAddr:       metricsAddr,
				TLSCertFile:       tlsCertFile,
				TLSKeyFile:        tlsKeyFile,
				NATSCAFile:        natsCAFile,
				NATSCertFile:      natsCertFile,
				NATSKeyFile:       natsKeyFile,
				KubeconfigPath:    kubeconfigPath,
			})
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address for ingest endpoints.")
	cmd.Flags().StringVar(&natsURL, "nats-url", "", "NATS JetStream URL (required).")
	cmd.Flags().StringVar(&natsSubjectPrefix, "nats-subject-prefix", "billing.usage", "NATS subject prefix.")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-bind-address", ":8081", "Health/readiness probe address.")
	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8443", "Prometheus metrics address (HTTPS). Set to 0 to disable.")
	cmd.Flags().StringVar(&tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file.")
	cmd.Flags().StringVar(&tlsKeyFile, "tls-key-file", "", "Path to TLS private key file.")
	cmd.Flags().StringVar(&natsCAFile, "nats-ca-file", "", "Path to NATS CA certificate file.")
	cmd.Flags().StringVar(&natsCertFile, "nats-cert-file", "", "Path to NATS client certificate file.")
	cmd.Flags().StringVar(&natsKeyFile, "nats-key-file", "", "Path to NATS client private key file.")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig-path", "", "Path to Milo kubeconfig. When set, drop usage for projects with no billing account before NATS.")
	_ = cmd.MarkFlagRequired("nats-url")

	zapFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	opts.BindFlags(zapFlags)
	cmd.Flags().AddGoFlagSet(zapFlags)

	return cmd
}
