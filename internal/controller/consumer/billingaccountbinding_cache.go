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

// BillingAccountBindingCache maintains a thread-safe in-memory index of the
// Active BillingAccountBinding per project, keyed by spec.projectRef.name and
// then by namespace. It registers event handlers on the controller-runtime
// informer so the index stays current via Watch stream without polling.
type BillingAccountBindingCache struct {
	mu              sync.RWMutex
	activeByProject map[string]map[string]*billingv1alpha1.BillingAccountBinding
	handlerReg      toolscache.ResourceEventHandlerRegistration
}

// NewBillingAccountBindingCache registers event handlers on the
// BillingAccountBinding informer and returns a cache ready to use once the
// manager cache syncs.
func NewBillingAccountBindingCache(ctx context.Context, c runtimecache.Cache) (*BillingAccountBindingCache, error) {
	bc := newBillingAccountBindingCache()

	informer, err := c.GetInformer(ctx, &billingv1alpha1.BillingAccountBinding{})
	if err != nil {
		return nil, fmt.Errorf("getting BillingAccountBinding informer: %w", err)
	}

	reg, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if b, ok := obj.(*billingv1alpha1.BillingAccountBinding); ok {
				bc.upsert(b)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if b, ok := newObj.(*billingv1alpha1.BillingAccountBinding); ok {
				bc.upsert(b)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if b, ok := obj.(*billingv1alpha1.BillingAccountBinding); ok {
				bc.delete(b)
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("adding BillingAccountBinding event handler: %w", err)
	}
	bc.handlerReg = reg

	return bc, nil
}

func newBillingAccountBindingCache() *BillingAccountBindingCache {
	return &BillingAccountBindingCache{
		activeByProject: make(map[string]map[string]*billingv1alpha1.BillingAccountBinding),
	}
}

// HasSynced reports whether the event handler has applied the informer's
// initial LIST. Informer store HasSynced is not enough: the secondary
// activeByProject index can still be empty after that returns.
func (b *BillingAccountBindingCache) HasSynced() bool {
	return b.handlerReg != nil && b.handlerReg.HasSynced()
}

func (b *BillingAccountBindingCache) upsert(binding *billingv1alpha1.BillingAccountBinding) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
		project := binding.Spec.ProjectRef.Name
		if b.activeByProject[project] == nil {
			b.activeByProject[project] = make(map[string]*billingv1alpha1.BillingAccountBinding)
		}
		b.activeByProject[project][binding.Namespace] = binding
		return
	}
	// Binding transitioned away from Active — evict it if it was the stored entry.
	b.evict(binding)
}

func (b *BillingAccountBindingCache) delete(binding *billingv1alpha1.BillingAccountBinding) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evict(binding)
}

func (b *BillingAccountBindingCache) evict(binding *billingv1alpha1.BillingAccountBinding) {
	project := binding.Spec.ProjectRef.Name
	byNamespace := b.activeByProject[project]
	if existing, ok := byNamespace[binding.Namespace]; ok && existing.Name == binding.Name {
		delete(byNamespace, binding.Namespace)
		if len(byNamespace) == 0 {
			delete(b.activeByProject, project)
		}
	}
}

// GetActive returns the Active BillingAccountBinding for the given project, or
// nil if no namespace, or more than one, holds an active binding for it.
func (b *BillingAccountBindingCache) GetActive(project string) *billingv1alpha1.BillingAccountBinding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	byNamespace := b.activeByProject[project]
	if len(byNamespace) != 1 {
		return nil
	}
	for _, binding := range byNamespace {
		return binding
	}
	return nil
}
