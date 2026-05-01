// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	natsgo "github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/config"
	"go.miloapis.com/billing/internal/controller"
	"go.miloapis.com/billing/internal/controller/consumer"
	billingwebhooks "go.miloapis.com/billing/internal/webhook/v1alpha1"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme, serializer.EnableStrict)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(config.AddToScheme(scheme))
	utilruntime.Must(config.RegisterDefaults(scheme))
	utilruntime.Must(billingv1alpha1.AddToScheme(scheme))
}

func newOperatorCommand(info BuildInfo) *cobra.Command {
	var (
		enableLeaderElection    bool
		leaderElectionNamespace string
		probeAddr               string
		serverConfigFile        string
	)

	opts := zap.Options{
		Development: true,
	}

	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Run the billing operator (controller-runtime manager)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

			setupLog := ctrl.Log.WithName("setup")
			setupLog.Info("starting billing operator",
				"version", info.Version,
				"gitCommit", info.GitCommit,
				"gitTreeState", info.GitTreeState,
				"buildDate", info.BuildDate,
			)

			var serverConfig config.BillingOperator
			var configData []byte
			if len(serverConfigFile) > 0 {
				var err error
				configData, err = os.ReadFile(serverConfigFile)
				if err != nil {
					return fmt.Errorf("reading server config from %q: %w", serverConfigFile, err)
				}
			}

			if err := runtime.DecodeInto(codecs.UniversalDecoder(), configData, &serverConfig); err != nil {
				return fmt.Errorf("decoding server config: %w", err)
			}

			setupLog.Info("server config", "config", serverConfig)

			cfg, err := serverConfig.RestConfig()
			if err != nil {
				return fmt.Errorf("loading rest config: %w", err)
			}

			ctx := ctrl.SetupSignalHandler()

			bootstrapClient, err := client.New(cfg, client.Options{Scheme: scheme})
			if err != nil {
				return fmt.Errorf("creating bootstrap client: %w", err)
			}

			metricsServerOptions := serverConfig.MetricsServer.Options(ctx, bootstrapClient)

			var webhookServer webhook.Server
			if serverConfig.WebhookServer != nil {
				webhookServer = webhook.NewServer(
					serverConfig.WebhookServer.Options(ctx, bootstrapClient),
				)
			} else {
				setupLog.Info("webhookServer not configured; admission webhook server disabled")
			}

			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme:                  scheme,
				Metrics:                 metricsServerOptions,
				WebhookServer:           webhookServer,
				HealthProbeBindAddress:  probeAddr,
				LeaderElection:          enableLeaderElection,
				LeaderElectionID:        "billing.miloapis.com",
				LeaderElectionNamespace: leaderElectionNamespace,
			})
			if err != nil {
				return fmt.Errorf("starting manager: %w", err)
			}

			if err = (&controller.BillingAccountReconciler{}).SetupWithManager(mgr); err != nil {
				return fmt.Errorf("creating BillingAccount controller: %w", err)
			}
			if err = (&controller.BillingAccountBindingReconciler{}).SetupWithManager(mgr); err != nil {
				return fmt.Errorf("creating BillingAccountBinding controller: %w", err)
			}
			if err = (&controller.MeterDefinitionReconciler{}).SetupWithManager(mgr); err != nil {
				return fmt.Errorf("creating MeterDefinition controller: %w", err)
			}
			if err = (&controller.MonitoredResourceTypeReconciler{}).SetupWithManager(mgr); err != nil {
				return fmt.Errorf("creating MonitoredResourceType controller: %w", err)
			}

			if err = controller.AddIndexers(ctx, mgr); err != nil {
				return fmt.Errorf("adding indexers: %w", err)
			}

			if err = controller.AddMeterDefinitionIndexers(ctx, mgr); err != nil {
				return fmt.Errorf("adding MeterDefinition indexers: %w", err)
			}

			// Register the UsageConsumer if NATS is configured (opt-in).
			if serverConfig.NATSConfig != nil {
				setupLog.Info("NATS config present; registering UsageConsumer",
					"url", serverConfig.NATSConfig.URL,
				)

				nc, err := natsgo.Connect(serverConfig.NATSConfig.URL,
					natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, err error) {
						setupLog.Error(err, "NATS disconnected")
					}),
					natsgo.ReconnectHandler(func(nc *natsgo.Conn) {
						setupLog.Info("NATS reconnected", "url", nc.ConnectedUrl())
					}),
					natsgo.ClosedHandler(func(_ *natsgo.Conn) {
						setupLog.Info("NATS connection closed")
					}),
				)
				if err != nil {
					return fmt.Errorf("connecting to NATS at %s: %w", serverConfig.NATSConfig.URL, err)
				}

				meterCache, err := consumer.NewMeterDefinitionCache(ctx, mgr.GetCache())
				if err != nil {
					return fmt.Errorf("creating MeterDefinition cache: %w", err)
				}

				bindingCache, err := consumer.NewBillingAccountBindingCache(ctx, mgr.GetCache())
				if err != nil {
					return fmt.Errorf("creating BillingAccountBinding cache: %w", err)
				}

				usageConsumer := &consumer.UsageConsumer{
					Cache:        mgr.GetCache(),
					NC:           nc,
					MeterCache:   meterCache,
					BindingCache: bindingCache,
					Logger:       ctrl.Log.WithName("usage-consumer"),
				}
				if err := mgr.Add(usageConsumer); err != nil {
					nc.Close()
					return fmt.Errorf("adding UsageConsumer to manager: %w", err)
				}

				// Close the NATS connection when the manager stops.
				// mgr.Add wraps the Runnable; we schedule the close via a
				// separate Runnable that blocks until ctx is done.
				if err := mgr.Add(natsCloser{nc: nc}); err != nil {
					return fmt.Errorf("adding NATS closer to manager: %w", err)
				}

				setupLog.Info("UsageConsumer registered")
			} else {
				setupLog.Info("natsConfig not set; UsageConsumer disabled")
			}

			if serverConfig.WebhookServer != nil {
				if err = billingwebhooks.SetupBillingAccountWebhookWithManager(mgr); err != nil {
					return fmt.Errorf("creating BillingAccount webhook: %w", err)
				}
				if err = billingwebhooks.SetupBillingAccountBindingWebhookWithManager(mgr); err != nil {
					return fmt.Errorf("creating BillingAccountBinding webhook: %w", err)
				}
				if err = billingwebhooks.SetupMeterDefinitionWebhookWithManager(mgr); err != nil {
					return fmt.Errorf("creating MeterDefinition webhook: %w", err)
				}
				if err = billingwebhooks.SetupMonitoredResourceTypeWebhookWithManager(mgr); err != nil {
					return fmt.Errorf("creating MonitoredResourceType webhook: %w", err)
				}
				if err = billingwebhooks.SetupMeterDefinitionOwnershipWebhookWithManager(mgr, "system:serviceaccount:services-system:services-operator"); err != nil {
					return fmt.Errorf("creating MeterDefinitionOwnership webhook: %w", err)
				}
				if err = billingwebhooks.SetupMonitoredResourceTypeOwnershipWebhookWithManager(mgr, "system:serviceaccount:services-system:services-operator"); err != nil {
					return fmt.Errorf("creating MonitoredResourceTypeOwnership webhook: %w", err)
				}
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return fmt.Errorf("setting up health check: %w", err)
			}
			if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				return fmt.Errorf("setting up ready check: %w", err)
			}

			setupLog.Info("starting manager")
			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("running manager: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	cmd.Flags().BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	cmd.Flags().StringVar(&leaderElectionNamespace, "leader-elect-namespace", "", "The namespace to use for leader election.")
	cmd.Flags().StringVar(&serverConfigFile, "server-config", "", "Path to the server config file.")

	// zap.Options.BindFlags accepts *flag.FlagSet (stdlib). Bridge via pflag's
	// AddGoFlagSet so the zap flags are surfaced on the cobra command.
	zapFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	opts.BindFlags(zapFlags)
	cmd.Flags().AddGoFlagSet(zapFlags)

	return cmd
}

// natsCloser is a manager.Runnable that closes the NATS connection when the
// manager context is cancelled, ensuring a clean shutdown after the consumer
// has stopped.
type natsCloser struct {
	nc *natsgo.Conn
}

func (n natsCloser) Start(ctx context.Context) error {
	<-ctx.Done()
	n.nc.Close()
	return nil
}
