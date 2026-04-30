// SPDX-License-Identifier: AGPL-3.0-only

// Package handler contains the HTTP handlers for the ingestion gateway.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"go.miloapis.com/billing/internal/gateway/auth"
	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

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
}

// extractBearerToken extracts the token from an "Authorization: Bearer <token>"
// header. Returns empty string if the header is missing or malformed.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

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

// AuthMiddleware returns an http.Handler middleware that verifies the bearer
// token on every request. The token value is never logged or reflected in any
// response body.
func AuthMiddleware(verifier auth.TokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "unauthorized")
			return
		}
		if err := verifier.Verify(r.Context(), token); err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
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
}

// NewIngestHandler creates a new IngestHandler.
func NewIngestHandler(publisher gwnats.Publisher, metrics Metrics, subjectPrefix string) *IngestHandler {
	return &IngestHandler{
		publisher:     publisher,
		metrics:       metrics,
		subjectPrefix: subjectPrefix,
	}
}

// BatchIngestHandler handles POST /v1/usage/events:batchIngest (batch ingest).
type BatchIngestHandler struct {
	publisher     gwnats.Publisher
	metrics       Metrics
	subjectPrefix string
	maxBatchSize  int
}

// NewBatchIngestHandler creates a new BatchIngestHandler.
func NewBatchIngestHandler(publisher gwnats.Publisher, metrics Metrics, subjectPrefix string) *BatchIngestHandler {
	return &BatchIngestHandler{
		publisher:     publisher,
		metrics:       metrics,
		subjectPrefix: subjectPrefix,
		maxBatchSize:  100,
	}
}
