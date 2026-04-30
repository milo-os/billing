// SPDX-License-Identifier: AGPL-3.0-only

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.miloapis.com/billing/internal/gateway/auth"
	"go.miloapis.com/billing/internal/gateway/handler"
	gwnats "go.miloapis.com/billing/internal/gateway/nats"
)

// --- fakes ---

type fakeVerifier struct{ err error }

func (f *fakeVerifier) Verify(_ context.Context, _ string) error { return f.err }

type fakePublisher struct {
	err     error
	healthy bool
	calls   []publishCall
}

type publishCall struct {
	subject string
	payload []byte
}

func (f *fakePublisher) Publish(_ context.Context, subject string, payload []byte) error {
	f.calls = append(f.calls, publishCall{subject: subject, payload: payload})
	return f.err
}

func (f *fakePublisher) Healthy() bool { return f.healthy }

var _ gwnats.Publisher = (*fakePublisher)(nil)
var _ gwnats.HealthChecker = (*fakePublisher)(nil)

type fakeMetrics struct {
	accepted []string
	rejected [][]string
}

func (m *fakeMetrics) RecordAccepted(_ context.Context, project string) {
	m.accepted = append(m.accepted, project)
}
func (m *fakeMetrics) RecordRejected(_ context.Context, project, reason string) {
	m.rejected = append(m.rejected, []string{project, reason})
}

// --- helpers ---

func validEventJSON(overrides map[string]any) []byte {
	base := map[string]any{
		"specversion":     "1.0",
		"type":            "com.example.usage",
		"source":          "/services/example",
		"subject":         "projects/p-abc123",
		"id":              "01HQ3YYZV3FDZM0B1NV1KPGWEP",
		"datacontenttype": "application/json",
		"data":            map[string]any{"value": "42"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	b, _ := json.Marshal(base)
	return b
}

func makeIngestRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/usage/events", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer valid-token")
	return req
}

func makeBatchRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/usage/events:batchIngest", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer valid-token")
	return req
}

func responseBody(t *testing.T, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	b, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return b
}

// --- auth middleware tests ---

func TestAuthMiddleware_missingHeader(t *testing.T) {
	verifier := &fakeVerifier{err: nil}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := handler.AuthMiddleware(verifier, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/usage/events", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_badToken(t *testing.T) {
	verifier := &fakeVerifier{err: auth.ErrTokenNotAuthenticated}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := handler.AuthMiddleware(verifier, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/usage/events", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer bad-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- IngestHandler tests ---

func TestIngestHandler_validEvent_200(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeIngestRequest(validEventJSON(nil)))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, responseBody(t, rec))
	}
	if len(pub.calls) != 1 {
		t.Errorf("expected 1 publish call, got %d", len(pub.calls))
	}
	if pub.calls[0].subject != "billing.usage.p-abc123.ingest" {
		t.Errorf("unexpected subject: %s", pub.calls[0].subject)
	}
	if len(m.accepted) != 1 || m.accepted[0] != "p-abc123" {
		t.Errorf("unexpected accepted metrics: %v", m.accepted)
	}
}

func TestIngestHandler_invalidULID_400(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeIngestRequest(validEventJSON(map[string]any{"id": "not-a-ulid"})))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if len(pub.calls) != 0 {
		t.Error("expected no publish on validation failure")
	}
}

func TestIngestHandler_missingRequiredField_400(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeIngestRequest(validEventJSON(map[string]any{"subject": nil})))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIngestHandler_natsTimeout_429(t *testing.T) {
	pub := &fakePublisher{healthy: true, err: context.DeadlineExceeded}
	m := &fakeMetrics{}
	h := handler.NewIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeIngestRequest(validEventJSON(nil)))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

func TestIngestHandler_natsDisconnect_503(t *testing.T) {
	pub := &fakePublisher{healthy: false, err: errors.New("nats: connection closed")}
	m := &fakeMetrics{}
	h := handler.NewIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeIngestRequest(validEventJSON(nil)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// --- BatchIngestHandler tests ---

func TestBatchIngestHandler_allValid_200(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewBatchIngestHandler(pub, m, "billing.usage")

	batch := []json.RawMessage{validEventJSON(nil), validEventJSON(nil)}
	body, _ := json.Marshal(batch)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeBatchRequest(body))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, responseBody(t, rec))
	}
	if len(pub.calls) != 2 {
		t.Errorf("expected 2 publish calls, got %d", len(pub.calls))
	}
}

func TestBatchIngestHandler_partialReject_207(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewBatchIngestHandler(pub, m, "billing.usage")

	batch := []json.RawMessage{
		validEventJSON(nil),
		validEventJSON(map[string]any{"id": "bad-ulid"}),
	}
	body, _ := json.Marshal(batch)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeBatchRequest(body))

	if rec.Code != http.StatusMultiStatus {
		t.Errorf("expected 207, got %d; body: %s", rec.Code, responseBody(t, rec))
	}

	var resp struct {
		Accepted int `json:"accepted"`
		Rejected []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"rejected"`
	}
	if err := json.Unmarshal(responseBody(t, rec), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Accepted != 1 {
		t.Errorf("expected accepted=1, got %d", resp.Accepted)
	}
	if len(resp.Rejected) != 1 {
		t.Errorf("expected 1 rejected event, got %d", len(resp.Rejected))
	}
}

func TestBatchIngestHandler_exceedsMax_400(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewBatchIngestHandler(pub, m, "billing.usage")

	batch := make([]json.RawMessage, 101)
	for i := range batch {
		batch[i] = validEventJSON(nil)
	}
	body, _ := json.Marshal(batch)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeBatchRequest(body))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestBatchIngestHandler_notArray_400(t *testing.T) {
	pub := &fakePublisher{healthy: true}
	m := &fakeMetrics{}
	h := handler.NewBatchIngestHandler(pub, m, "billing.usage")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, makeBatchRequest(validEventJSON(nil)))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- health handler tests ---

func TestHealthHandler_200(t *testing.T) {
	h := handler.NewHealthHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestReadyHandler_healthy_200(t *testing.T) {
	checker := &fakePublisher{healthy: true}
	h := handler.NewReadyHandler(checker)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestReadyHandler_unhealthy_503(t *testing.T) {
	checker := &fakePublisher{healthy: false}
	h := handler.NewReadyHandler(checker)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}
