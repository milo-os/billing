// SPDX-License-Identifier: AGPL-3.0-only

package handler

import (
	"net/http"

	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

// HealthHandler serves GET /healthz — always returns 200 OK.
type HealthHandler struct{}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// ServeHTTP always returns 200 OK when the process is alive.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ReadyHandler serves GET /readyz — returns 200 if NATS is healthy, 503 otherwise.
type ReadyHandler struct {
	checker gwnats.HealthChecker
}

// NewReadyHandler creates a ReadyHandler that uses checker to determine
// NATS connection health.
func NewReadyHandler(checker gwnats.HealthChecker) *ReadyHandler {
	return &ReadyHandler{checker: checker}
}

// ServeHTTP returns 200 OK if the NATS connection is healthy, 503 otherwise.
func (h *ReadyHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if !h.checker.Healthy() {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "NATS connection is not healthy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
