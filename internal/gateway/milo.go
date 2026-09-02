// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
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
// must point at the Milo API server.
//
// The informer cache is started against ctx. WaitForCacheSync is not enough
// on its own: that is store sync, not handler sync, so the secondary indexes
// can still be empty. We also wait on the AddEventHandler registrations.
// If the cache later stops with an error, fatal receives it so Run can exit
// and kubelet restarts a fresh index.
func startMiloAttributor(ctx context.Context, kubeconfigPath string, fatal chan<- error) (handler.Attributor, error) {
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
			if ctx.Err() != nil {
				return
			}
			serverLog.Error(err, "milo cache stopped")
			select {
			case fatal <- fmt.Errorf("milo cache: %w", err):
			case <-ctx.Done():
			}
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for milo cache sync: %w", err)
		}
		return nil, fmt.Errorf("waiting for milo cache sync")
	}
	// Store HasSynced does not mean the binding/account indexes have seen
	// the initial LIST. Bound() is fail-closed, so ingest must not start
	// until the handlers have applied that list.
	if !cache.WaitForNamedCacheSync("milo-attributor", ctx.Done(), bindings.HasSynced, accounts.HasSynced) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for milo handler sync: %w", err)
		}
		return nil, fmt.Errorf("waiting for milo handler sync")
	}
	serverLog.Info("milo billing account cache synced")
	return miloAttributor{bindings: bindings, accounts: accounts}, nil
}
