// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"context"
	"fmt"
	"sync"

	toolscache "k8s.io/client-go/tools/cache"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

// BillingAccountCache maintains a thread-safe in-memory index of Ready
// BillingAccounts keyed by "namespace/name". It registers event handlers on
// the controller-runtime informer so the index stays current via Watch stream
// without polling.
type BillingAccountCache struct {
	mu         sync.RWMutex
	readyByKey map[string]*billingv1alpha1.BillingAccount
	handlerReg toolscache.ResourceEventHandlerRegistration
}

// NewBillingAccountCache registers event handlers on the BillingAccount
// informer and returns a cache ready to use once the manager cache syncs.
func NewBillingAccountCache(ctx context.Context, c runtimecache.Cache) (*BillingAccountCache, error) {
	ac := &BillingAccountCache{
		readyByKey: make(map[string]*billingv1alpha1.BillingAccount),
	}

	informer, err := c.GetInformer(ctx, &billingv1alpha1.BillingAccount{})
	if err != nil {
		return nil, fmt.Errorf("getting BillingAccount informer: %w", err)
	}

	reg, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if a, ok := obj.(*billingv1alpha1.BillingAccount); ok {
				ac.upsert(a)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if a, ok := newObj.(*billingv1alpha1.BillingAccount); ok {
				ac.upsert(a)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if a, ok := obj.(*billingv1alpha1.BillingAccount); ok {
				ac.delete(a)
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("adding BillingAccount event handler: %w", err)
	}
	ac.handlerReg = reg

	return ac, nil
}

// HasSynced reports whether the event handler has applied the informer's
// initial LIST. Informer store HasSynced is not enough: the secondary
// readyByKey index can still be empty after that returns.
func (a *BillingAccountCache) HasSynced() bool {
	return a.handlerReg != nil && a.handlerReg.HasSynced()
}

func (a *BillingAccountCache) upsert(account *billingv1alpha1.BillingAccount) {
	key := account.Namespace + "/" + account.Name
	a.mu.Lock()
	defer a.mu.Unlock()
	if account.Status.Phase == billingv1alpha1.BillingAccountPhaseReady {
		a.readyByKey[key] = account
		return
	}
	delete(a.readyByKey, key)
}

func (a *BillingAccountCache) delete(account *billingv1alpha1.BillingAccount) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.readyByKey, account.Namespace+"/"+account.Name)
}

// GetReady returns the BillingAccount with the given namespace and name if it
// is Ready, or nil if the account does not exist or is not in the Ready phase.
func (a *BillingAccountCache) GetReady(namespace, name string) *billingv1alpha1.BillingAccount {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.readyByKey[namespace+"/"+name]
}
