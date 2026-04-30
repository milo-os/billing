// SPDX-License-Identifier: AGPL-3.0-only

package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"go.miloapis.com/billing/internal/gateway/validate"
)

// ServeHTTP handles POST /v1/usage/events:batchIngest.
// Accepts a JSON array of CloudEvents (max 100).
// Returns:
//   - 200 OK with {"accepted": N} when all events are accepted.
//   - 207 Multi-Status when at least one event fails structural validation.
//   - 400 Bad Request if body is not a valid JSON array or exceeds 100 events.
//   - 401 Unauthorized (enforced by authMiddleware upstream).
//   - 429 Too Many Requests for NATS backpressure.
func (h *BatchIngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize*100))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
		return
	}

	var rawEvents []json.RawMessage
	if err := json.Unmarshal(body, &rawEvents); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be a JSON array of CloudEvents")
		return
	}
	if len(rawEvents) > h.maxBatchSize {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"batch size exceeds maximum of 100 events")
		return
	}

	var rejected []rejectedEvent
	accepted := 0

	for _, raw := range rawEvents {
		result := validate.ValidateEvent(raw)
		if !result.Valid {
			project := projectFrom(cloudEventSubjectFromRaw(raw))
			h.metrics.RecordRejected(r.Context(), project, string(result.Reason))
			rejected = append(rejected, rejectedEvent{
				ID:     result.ID,
				Reason: string(result.Reason),
				Detail: result.Detail,
			})
			continue
		}

		subject := subjectFor(h.subjectPrefix, cloudEventSubjectFromRaw(raw))
		if err := h.publisher.Publish(r.Context(), subject, raw); err != nil {
			// On NATS failure, stop processing and return the appropriate error.
			writePublishError(w, err)
			return
		}
		h.metrics.RecordAccepted(r.Context(), projectFrom(cloudEventSubjectFromRaw(raw)))
		accepted++
	}

	resp := batchIngestResponse{
		Accepted: accepted,
		Rejected: rejected,
	}

	if len(rejected) > 0 {
		writeJSON(w, http.StatusMultiStatus, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// cloudEventSubjectFromRaw extracts the subject field from a raw CloudEvent
// JSON message. Returns empty string on failure.
func cloudEventSubjectFromRaw(raw json.RawMessage) string {
	var ce struct {
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(raw, &ce)
	return ce.Subject
}
