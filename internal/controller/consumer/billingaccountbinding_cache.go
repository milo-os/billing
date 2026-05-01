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
// Active BillingAccountBinding per project keyed by spec.projectRef.name. It
// registers event handlers on the controller-runtime informer so the index
// stays current via Watch stream without polling.
type BillingAccountBindingCache struct {
	mu              sync.RWMutex
	activeByProject map[string]*billingv1alpha1.BillingAccountBinding
}

// NewBillingAccountBindingCache registers event handlers on the
// BillingAccountBinding informer and returns a cache ready to use once the
// manager cache syncs.
func NewBillingAccountBindingCache(ctx context.Context, c runtimecache.Cache) (*BillingAccountBindingCache, error) {
	bc := &BillingAccountBindingCache{
		activeByProject: make(map[string]*billingv1alpha1.BillingAccountBinding),
	}

	informer, err := c.GetInformer(ctx, &billingv1alpha1.BillingAccountBinding{})
	if err != nil {
		return nil, fmt.Errorf("getting BillingAccountBinding informer: %w", err)
	}

	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
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
	}); err != nil {
		return nil, fmt.Errorf("adding BillingAccountBinding event handler: %w", err)
	}

	return bc, nil
}

func (b *BillingAccountBindingCache) upsert(binding *billingv1alpha1.BillingAccountBinding) {
	project := binding.Spec.ProjectRef.Name
	b.mu.Lock()
	defer b.mu.Unlock()
	if binding.Status.Phase == billingv1alpha1.BillingAccountBindingPhaseActive {
		b.activeByProject[project] = binding
		return
	}
	// Binding transitioned away from Active — evict it if it was the stored entry.
	if existing, ok := b.activeByProject[project]; ok && existing.Name == binding.Name {
		delete(b.activeByProject, project)
	}
}

func (b *BillingAccountBindingCache) delete(binding *billingv1alpha1.BillingAccountBinding) {
	project := binding.Spec.ProjectRef.Name
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.activeByProject[project]; ok && existing.Name == binding.Name {
		delete(b.activeByProject, project)
	}
}

// GetActive returns the Active BillingAccountBinding for the given project, or
// nil if no active binding exists.
func (b *BillingAccountBindingCache) GetActive(project string) *billingv1alpha1.BillingAccountBinding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.activeByProject[project]
}
