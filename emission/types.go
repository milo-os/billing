// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// UsageEvent is a billable usage event to be recorded by the Emission SDK.
//
// All fields except Dimensions, Resource, and OccurredAt are required.
// Record() returns a ValidationError if a required field is absent or
// malformed.
//
// OccurredAt defaults to time.Now() captured at Record() call time when the
// zero value is provided. This minimises timestamp skew for callers that
// construct UsageEvent ahead of the actual Record() call.
type UsageEvent struct {
	// Meter is the canonical meter name (e.g.
	// "compute.miloapis.com/instance/cpu-seconds"). Must be non-empty.
	Meter string

	// Project identifies the project responsible for the usage.
	Project ProjectRef

	// Source identifies the component emitting this event. Must be a
	// valid absolute URI (e.g. "//compute.miloapis.com/controllers/reconciler").
	Source string

	// Quantity is the number of units consumed. Must be greater than zero.
	Quantity int64

	// Dimensions is an optional map of dimension key/value pairs for
	// this event (e.g. {"region": "us-central1"}). May be nil or empty.
	Dimensions map[string]string

	// Resource optionally identifies the Kubernetes object that generated
	// this usage. May be nil.
	Resource *ResourceRef

	// OccurredAt is when the usage occurred. When zero, Record() defaults
	// to time.Now() at call time.
	OccurredAt time.Time
}

// ProjectRef identifies the project responsible for the usage.
type ProjectRef struct {
	// Name is the resource name of the project in projects/{id} format
	// (e.g. "projects/p-abc"). Must be non-empty and match the pattern.
	Name string
}

// ResourceRef identifies the Kubernetes object that generated the usage.
type ResourceRef struct {
	// Group is the Kubernetes API group (e.g. "compute.miloapis.com").
	Group string

	// Kind is the Kubernetes Kind (e.g. "Instance").
	Kind string

	// Namespace is the namespace of the object. Empty for cluster-scoped
	// resources.
	Namespace string

	// Name is the object's metadata.name.
	Name string

	// UID is the object's metadata.uid for stable cross-version identity.
	UID types.UID
}

// Recorder is the entry point for emitting usage events to the billing
// pipeline.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// Record() is synchronous: it returns only after the event has been accepted
// by the downstream sink (Vector Agent) or after the retry budget is
// exhausted.
//
// Callers must handle non-nil errors via their own retry mechanism (e.g.,
// controller re-queue). The SDK never buffers events in memory after
// Record() returns an error.
type Recorder interface {
	Record(ctx context.Context, event UsageEvent) error
}

// NoopRecorder is a Recorder implementation that silently discards all
// events. Use it in test contexts where usage recording should succeed
// without side effects.
type NoopRecorder struct{}

func (NoopRecorder) Record(_ context.Context, _ UsageEvent) error { return nil }
