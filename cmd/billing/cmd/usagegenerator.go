// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.miloapis.com/billing/emission"
	"go.miloapis.com/billing/internal/config"
	"go.miloapis.com/billing/internal/usagegenerator"
)

func newUsageGeneratorCommand() *cobra.Command {
	var (
		serverConfigFile string
		probeAddr        string
		endpoint         string
		interval         time.Duration
		bindings         []string
		meters           []string
	)

	opts := zap.Options{Development: true}

	cmd := &cobra.Command{
		Use:   "usage-generator",
		Short: "Run the usage generator (dev/staging only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
			log := ctrl.Log.WithName("setup")

			if endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}

			var serverConfig config.BillingOperator
			var configData []byte
			if serverConfigFile != "" {
				var err error
				configData, err = os.ReadFile(serverConfigFile)
				if err != nil {
					return fmt.Errorf("reading server config from %q: %w", serverConfigFile, err)
				}
			}
			if err := runtime.DecodeInto(codecs.UniversalDecoder(), configData, &serverConfig); err != nil {
				return fmt.Errorf("decoding server config: %w", err)
			}

			cfg, err := serverConfig.RestConfig()
			if err != nil {
				return fmt.Errorf("loading rest config: %w", err)
			}

			includedBindings := make([]types.NamespacedName, 0, len(bindings))
			for _, ref := range bindings {
				ns, name, ok := strings.Cut(ref, "/")
				if !ok {
					return fmt.Errorf("invalid --bindings %q: expected namespace/name", ref)
				}
				includedBindings = append(includedBindings, types.NamespacedName{Namespace: ns, Name: name})
			}

			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme:                 scheme,
				HealthProbeBindAddress: probeAddr,
			})
			if err != nil {
				return fmt.Errorf("creating manager: %w", err)
			}

			recorder, err := emission.NewUsageRecorder(emission.WithEndpoint(endpoint))
			if err != nil {
				return fmt.Errorf("creating usage recorder: %w", err)
			}

			generator := &usagegenerator.UsageGenerator{
				Client:           mgr.GetClient(),
				Recorder:         recorder,
				Interval:         interval,
				Meters:           meters,
				IncludedBindings: includedBindings,
				Logger:           ctrl.Log.WithName("usage-generator"),
			}
			if err := mgr.Add(generator); err != nil {
				return fmt.Errorf("adding UsageGenerator: %w", err)
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return fmt.Errorf("setting up health check: %w", err)
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				return fmt.Errorf("setting up ready check: %w", err)
			}

			log.Info("starting usage-generator",
				"endpoint", endpoint,
				"interval", interval,
				"bindings", bindings,
				"meters", meters,
			)

			ctx := ctrl.SetupSignalHandler()
			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("running manager: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serverConfigFile, "server-config", "", "Path to the BillingOperator config file.")
	cmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "HTTP endpoint to emit usage events (required).")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Interval between emission ticks.")
	cmd.Flags().StringSliceVar(&bindings, "bindings", nil,
		"BillingAccountBindings to emit usage for, as namespace/name pairs.")
	cmd.Flags().StringSliceVar(&meters, "meters", nil,
		"Meter names to emit. Empty uses built-in defaults.")

	zapFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	opts.BindFlags(zapFlags)
	cmd.Flags().AddGoFlagSet(zapFlags)

	return cmd
}
