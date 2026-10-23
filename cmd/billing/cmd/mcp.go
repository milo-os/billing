// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"

	"go.miloapis.com/billing/internal/mcpserver"
)

func newMCPCommand(info BuildInfo) *cobra.Command {
	var (
		addr        string
		kubeconfig  string
		kubeContext string
	)

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve billing's usage tools to Datum's assistant over MCP",
		Long: `Serve billing's usage tools, knowledge and skills to Datum's assistant (Patch)
over Streamable HTTP MCP.

The server holds no credential: each request reads through Milo with the
caller's bearer token, in the project named by the X-Datum-Project header.
--kubeconfig names the Milo API server and its CA; any credentials in it are
ignored.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rules := clientcmd.NewDefaultClientConfigLoadingRules()
			rules.ExplicitPath = kubeconfig
			overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
			raw, err := rules.Load()
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}
			// No in-cluster fallback: this server reads Datum control planes
			// through Milo, never the cluster it runs in.
			base, err := clientcmd.NewDefaultClientConfig(*raw, overrides).ClientConfig()
			if err != nil {
				return fmt.Errorf("building configuration from kubeconfig (it must name the Milo API "+
					"server and CA): %w", err)
			}

			handler, err := mcpserver.NewHandler(mcpserver.Options{
				BaseConfig: base,
				Name:       "billing",
				Version:    info.Version,
			})
			if err != nil {
				return err
			}
			// Drain in-flight tool calls on SIGTERM instead of cutting them off.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return mcpserver.Serve(ctx, addr, handler)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address.")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Kubeconfig naming the Milo API server and CA (default: KUBECONFIG, then ~/.kube/config).")
	cmd.Flags().StringVar(&kubeContext, "context", "", "Kubeconfig context to use.")
	return cmd
}
