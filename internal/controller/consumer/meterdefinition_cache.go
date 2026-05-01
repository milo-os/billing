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

// MeterDefinitionCache maintains a thread-safe in-memory index of active
// MeterDefinitions keyed by spec.meterName. It registers event handlers on the
// controller-runtime informer so the index stays current via Watch stream
// without polling.
type MeterDefinitionCache struct {
	mu          sync.RWMutex
	byMeterName map[string]*billingv1alpha1.MeterDefinition
}

// NewMeterDefinitionCache registers event handlers on the MeterDefinition
// informer and returns a cache ready to use once the manager cache syncs.
func NewMeterDefinitionCache(ctx context.Context, c runtimecache.Cache) (*MeterDefinitionCache, error) {
	mc := &MeterDefinitionCache{
		byMeterName: make(map[string]*billingv1alpha1.MeterDefinition),
	}

	informer, err := c.GetInformer(ctx, &billingv1alpha1.MeterDefinition{})
	if err != nil {
		return nil, fmt.Errorf("getting MeterDefinition informer: %w", err)
	}

	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if md, ok := obj.(*billingv1alpha1.MeterDefinition); ok {
				mc.upsert(md)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if md, ok := newObj.(*billingv1alpha1.MeterDefinition); ok {
				mc.upsert(md)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if md, ok := obj.(*billingv1alpha1.MeterDefinition); ok {
				mc.delete(md)
			}
		},
	}); err != nil {
		return nil, fmt.Errorf("adding MeterDefinition event handler: %w", err)
	}

	return mc, nil
}

func (m *MeterDefinitionCache) upsert(md *billingv1alpha1.MeterDefinition) {
	if md.Spec.Phase != billingv1alpha1.PhasePublished && md.Spec.Phase != billingv1alpha1.PhaseDeprecated {
		m.delete(md)
		return
	}
	m.mu.Lock()
	m.byMeterName[md.Spec.MeterName] = md
	m.mu.Unlock()
}

func (m *MeterDefinitionCache) delete(md *billingv1alpha1.MeterDefinition) {
	m.mu.Lock()
	delete(m.byMeterName, md.Spec.MeterName)
	m.mu.Unlock()
}

// Get returns the MeterDefinition for the given meter name, or nil if no
// Published or Deprecated MeterDefinition exists for that name.
func (m *MeterDefinitionCache) Get(meterName string) *billingv1alpha1.MeterDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byMeterName[meterName]
}
