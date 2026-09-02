// SPDX-License-Identifier: AGPL-3.0-only

// Package handler contains the HTTP handlers for the ingestion gateway.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

var log = ctrl.Log.WithName("handler")

// ingestResponse is the JSON body for a successful single-event ingest.
type ingestResponse struct {
	Accepted int `json:"accepted"`
}

// batchIngestResponse is the JSON body for batch ingest (200 OK or 207).
type batchIngestResponse struct {
	Accepted int             `json:"accepted"`
	Rejected []rejectedEvent `json:"rejected,omitempty"`
}

// rejectedEvent describes a single rejected event in a batch response.
type rejectedEvent struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// errorResponse is the JSON body for 4xx/5xx responses.
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Metrics is the interface required by handlers to record counters.
type Metrics interface {
	RecordAccepted(ctx context.Context, project string)
	RecordRejected(ctx context.Context, project, reason string)
	RecordDropped(ctx context.Context, project, reason string)
}

// Attributor reports whether a project currently has a billable account.
// A nil Attributor publishes every structurally valid event (dev/e2e).
type Attributor interface {
	Bound(project string) bool
}

// BoundFunc adapts a function to Attributor.
type BoundFunc func(project string) bool

// Bound implements Attributor.
func (f BoundFunc) Bound(project string) bool { return f(project) }

// DropReasonAttributionFailure is recorded when a structurally valid event
// is dropped because the project has no Active BillingAccountBinding (or
// the bound account is not Ready). Matches the usage-consumer reason so
// dashboards can join gateway drops with consumer rejections.
const DropReasonAttributionFailure = "attribution_failure"

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a structured error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

// subjectFor derives the NATS subject from the CloudEvent subject field and the
// configured prefix. e.g. prefix="billing.usage", subject="projects/p-abc" →
// "billing.usage.p-abc.ingest".
func subjectFor(prefix, cloudEventSubject string) string {
	projectID := strings.TrimPrefix(cloudEventSubject, "projects/")
	return prefix + "." + projectID + ".ingest"
}

// projectFrom extracts the project ID from a CloudEvent subject, returning
// "unknown" if the subject is absent or malformed.
func projectFrom(cloudEventSubject string) string {
	if !strings.HasPrefix(cloudEventSubject, "projects/") {
		return "unknown"
	}
	p := strings.TrimPrefix(cloudEventSubject, "projects/")
	if p == "" {
		return "unknown"
	}
	return p
}

// IngestHandler handles POST /v1/usage/events (single event ingest).
type IngestHandler struct {
	publisher     gwnats.Publisher
	metrics       Metrics
	subjectPrefix string
	attributor    Attributor
}

// NewIngestHandler creates a new IngestHandler. attributor may be nil.
func NewIngestHandler(publisher gwnats.Publisher, metrics Metrics, subjectPrefix string, attributor Attributor) *IngestHandler {
	return &IngestHandler{
		publisher:     publisher,
		metrics:       metrics,
		subjectPrefix: subjectPrefix,
		attributor:    attributor,
	}
}

// BatchIngestHandler handles POST /v1/usage/events:batchIngest (batch ingest).
type BatchIngestHandler struct {
	publisher     gwnats.Publisher
	metrics       Metrics
	subjectPrefix string
	maxBatchSize  int
	attributor    Attributor
}

// NewBatchIngestHandler creates a new BatchIngestHandler. attributor may be nil.
func NewBatchIngestHandler(publisher gwnats.Publisher, metrics Metrics, subjectPrefix string, attributor Attributor) *BatchIngestHandler {
	return &BatchIngestHandler{
		publisher:     publisher,
		metrics:       metrics,
		subjectPrefix: subjectPrefix,
		maxBatchSize:  100,
		attributor:    attributor,
	}
}

func dropUnbound(ctx context.Context, attr Attributor, metrics Metrics, project string) bool {
	if attr == nil || attr.Bound(project) {
		return false
	}
	metrics.RecordDropped(ctx, project, DropReasonAttributionFailure)
	log.V(1).Info("dropping unbound usage event",
		"project", project,
		"reason", DropReasonAttributionFailure,
	)
	return true
}
