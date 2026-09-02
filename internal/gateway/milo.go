// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/controller/consumer"
	"go.miloapis.com/billing/internal/gateway/handler"
)

type miloAttributor struct {
	bindings *consumer.BillingAccountBindingCache
	accounts *consumer.BillingAccountCache
}

func (a miloAttributor) Bound(project string) bool {
	return consumer.Bound(project, a.bindings, a.accounts)
}

// startMiloAttributor watches BillingAccountBinding and BillingAccount on
// Milo and returns an Attributor used to drop unbillable traffic. kubeconfigPath
// must point at the Milo API server. The cache is started against ctx and
// must finish syncing before the ingest server accepts traffic.
func startMiloAttributor(ctx context.Context, kubeconfigPath string) (handler.Attributor, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading milo kubeconfig from %q: %w", kubeconfigPath, err)
	}

	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding billing types to scheme: %w", err)
	}

	c, err := runtimecache.New(restCfg, runtimecache.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating milo cache: %w", err)
	}

	bindings, err := consumer.NewBillingAccountBindingCache(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("creating billing account binding cache: %w", err)
	}
	accounts, err := consumer.NewBillingAccountCache(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("creating billing account cache: %w", err)
	}

	go func() {
		if err := c.Start(ctx); err != nil {
			serverLog.Error(err, "milo cache stopped")
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, fmt.Errorf("waiting for milo cache sync")
	}
	serverLog.Info("milo billing account cache synced")
	return miloAttributor{bindings: bindings, accounts: accounts}, nil
}
