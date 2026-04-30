// SPDX-License-Identifier: AGPL-3.0-only

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"go.miloapis.com/billing/internal/gateway/validate"
)

const maxBodySize = 1 << 20 // 1 MiB

// ServeHTTP handles POST /v1/usage/events.
// Steps:
//  1. Read body (max 1 MiB)
//  2. Validate structural correctness
//  3. Publish to NATS JetStream
//  4. Return 200 {"accepted": 1} on success
func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
		return
	}

	result := validate.ValidateEvent(json.RawMessage(body))
	if !result.Valid {
		project := projectFrom("")
		// Try to extract project from the partially parsed result.
		h.metrics.RecordRejected(r.Context(), project, string(result.Reason))
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Code:    string(result.Reason),
			Message: result.Detail,
		})
		return
	}

	subject := subjectFor(h.subjectPrefix, cloudEventSubjectFromBody(body))
	if err := h.publish(r.Context(), subject, body); err != nil {
		writePublishError(w, err)
		return
	}

	h.metrics.RecordAccepted(r.Context(), projectFrom(cloudEventSubjectFromBody(body)))
	writeJSON(w, http.StatusOK, ingestResponse{Accepted: 1})
}

// publish wraps publisher.Publish and translates errors to HTTP status codes.
func (h *IngestHandler) publish(ctx context.Context, subject string, payload []byte) error {
	return h.publisher.Publish(ctx, subject, payload)
}

// writePublishError writes the appropriate HTTP response for a NATS publish error.
func writePublishError(w http.ResponseWriter, err error) {
	// context.DeadlineExceeded → 429, connection errors → 503.
	if isTimeoutError(err) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "publish timeout; try again later")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "upstream unavailable")
}

// isTimeoutError returns true for context deadline exceeded errors and
// NATS timeout errors.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// context.DeadlineExceeded is the canonical timeout.
	if err == context.DeadlineExceeded {
		return true
	}
	// NATS JetStream surfaces timeout as an error with "timeout" in the message.
	// Use string check to avoid importing nats internals.
	return containsTimeout(err.Error())
}

func containsTimeout(msg string) bool {
	return len(msg) >= 7 && containsSubstring(msg, "timeout")
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// cloudEventSubjectFromBody extracts the subject field from a raw CloudEvent
// JSON body without allocating a full struct. Returns empty string on failure.
func cloudEventSubjectFromBody(body []byte) string {
	var ce struct {
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(body, &ce)
	return ce.Subject
}

