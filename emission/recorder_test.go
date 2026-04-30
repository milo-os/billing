// SPDX-License-Identifier: AGPL-3.0-only

package emission_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"k8s.io/apimachinery/pkg/types"

	"go.miloapis.com/billing/emission"
)

// validEvent returns a minimal valid UsageEvent for reuse across tests.
func validEvent() emission.UsageEvent {
	return emission.UsageEvent{
		Meter:    "compute.miloapis.com/instance/cpu-seconds",
		Project:  emission.ProjectRef{Name: "projects/p-abc"},
		Source:   "//compute.miloapis.com/controllers/instance-reconciler",
		Quantity: 42,
	}
}

// newTestProvider creates a MeterProvider backed by a ManualReader for
// inspecting counter values in tests.
func newTestProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return mp, reader
}

// collectCounterValue collects all metrics from the reader and returns the
// cumulative value of the counter with the given name. It sums over all
// data points (attribute sets).
func collectCounterValue(t *testing.T, reader *sdkmetric.ManualReader, counterName string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != counterName {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// --- Validation tests ---

func TestRecord_ValidationErrors(t *testing.T) {
	mp, _ := newTestProvider()

	r, err := emission.NewVectorRecorder(emission.WithMeterProvider(mp))
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*emission.UsageEvent)
		wantErr string
	}{
		{
			name:    "empty Meter",
			mutate:  func(ev *emission.UsageEvent) { ev.Meter = "" },
			wantErr: "Meter",
		},
		{
			name:    "empty Project.Name",
			mutate:  func(ev *emission.UsageEvent) { ev.Project.Name = "" },
			wantErr: "Project.Name",
		},
		{
			name:    "malformed Project.Name",
			mutate:  func(ev *emission.UsageEvent) { ev.Project.Name = "bad-format" },
			wantErr: "Project.Name",
		},
		{
			name:    "project name with extra slash",
			mutate:  func(ev *emission.UsageEvent) { ev.Project.Name = "projects/a/b" },
			wantErr: "Project.Name",
		},
		{
			name:    "empty Source",
			mutate:  func(ev *emission.UsageEvent) { ev.Source = "" },
			wantErr: "Source",
		},
		{
			name:    "relative Source URI",
			mutate:  func(ev *emission.UsageEvent) { ev.Source = "not-a-uri" },
			wantErr: "Source",
		},
		{
			name:    "zero Quantity",
			mutate:  func(ev *emission.UsageEvent) { ev.Quantity = 0 },
			wantErr: "Quantity",
		},
		{
			name:    "negative Quantity",
			mutate:  func(ev *emission.UsageEvent) { ev.Quantity = -1 },
			wantErr: "Quantity",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := validEvent()
			tc.mutate(&ev)
			err := r.Record(context.Background(), ev)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			var ve *emission.ValidationError
			if !errorAs(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(ve.Field, tc.wantErr) {
				t.Errorf("expected Field to contain %q, got %q", tc.wantErr, ve.Field)
			}
		})
	}
}

func TestRecord_ValidationSucceeds_ValidProjectNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	r, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	valid := []string{"projects/p-abc", "projects/123", "projects/my-project-id"}
	for _, name := range valid {
		ev := validEvent()
		ev.Project.Name = name
		if err := r.Record(context.Background(), ev); err != nil {
			t.Errorf("project name %q: unexpected error: %v", name, err)
		}
	}
}

// --- CloudEvents field mapping tests ---

func TestRecord_CloudEventsMapping(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	occurredAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	ev := emission.UsageEvent{
		Meter:      "compute.miloapis.com/instance/cpu-seconds",
		Project:    emission.ProjectRef{Name: "projects/p-abc"},
		Source:     "//compute.miloapis.com/controllers/instance-reconciler",
		Quantity:   42,
		OccurredAt: occurredAt,
		Dimensions: map[string]string{"region": "us-central1"},
		Resource: &emission.ResourceRef{
			Group:     "compute.miloapis.com",
			Kind:      "Instance",
			Namespace: "default",
			Name:      "my-instance",
			UID:       types.UID("uid-123"),
		},
	}

	before := time.Now()
	if err := rec.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record: %v", err)
	}
	after := time.Now()
	_ = before
	_ = after

	// Verify Content-Type header.
	if ct := captured.Header.Get("Content-Type"); ct != "application/cloudevents+json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/cloudevents+json")
	}

	// Parse the CloudEvents envelope.
	var ce map[string]interface{}
	if err := json.Unmarshal(capturedBody, &ce); err != nil {
		t.Fatalf("parsing CE body: %v", err)
	}

	assertEqual(t, "specversion", "1.0", ce["specversion"])
	assertEqual(t, "type", ev.Meter, ce["type"])
	assertEqual(t, "source", ev.Source, ce["source"])
	assertEqual(t, "subject", ev.Project.Name, ce["subject"])
	assertEqual(t, "datacontenttype", "application/json", ce["datacontenttype"])

	// id must be present and non-empty.
	id, ok := ce["id"].(string)
	if !ok || id == "" {
		t.Errorf("id field missing or empty")
	}

	// time must match OccurredAt.
	ceTime, ok := ce["time"].(string)
	if !ok {
		t.Errorf("time field missing or not a string")
	} else {
		parsed, err := time.Parse(time.RFC3339, ceTime)
		if err != nil {
			t.Errorf("time field not RFC 3339: %v", err)
		} else if !parsed.Equal(occurredAt) {
			t.Errorf("time = %v, want %v", parsed, occurredAt)
		}
	}

	// Parse data payload.
	dataRaw, ok := ce["data"]
	if !ok {
		t.Fatal("data field missing")
	}
	dataBytes, _ := json.Marshal(dataRaw)
	var data map[string]interface{}
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("parsing data: %v", err)
	}

	assertEqual(t, "data.value", "42", data["value"])

	dims, ok := data["dimensions"].(map[string]interface{})
	if !ok {
		t.Fatal("data.dimensions missing or wrong type")
	}
	assertEqual(t, "data.dimensions.region", "us-central1", dims["region"])

	res, ok := data["resource"].(map[string]interface{})
	if !ok {
		t.Fatal("data.resource missing or wrong type")
	}
	assertEqual(t, "data.resource.group", "compute.miloapis.com", res["group"])
	assertEqual(t, "data.resource.kind", "Instance", res["kind"])
	assertEqual(t, "data.resource.namespace", "default", res["namespace"])
	assertEqual(t, "data.resource.name", "my-instance", res["name"])
	assertEqual(t, "data.resource.uid", "uid-123", res["uid"])
}

func TestRecord_OccurredAt_DefaultsToNow(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	ev := validEvent()
	// OccurredAt is zero; must default to time.Now() at Record() call time.

	before := time.Now().Truncate(time.Second)
	if err := rec.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record: %v", err)
	}
	after := time.Now().Add(time.Second)

	var ce map[string]interface{}
	if err := json.Unmarshal(capturedBody, &ce); err != nil {
		t.Fatalf("parsing CE body: %v", err)
	}

	ceTime, ok := ce["time"].(string)
	if !ok {
		t.Fatal("time field missing")
	}
	parsed, err := time.Parse(time.RFC3339, ceTime)
	if err != nil {
		t.Fatalf("time field not RFC 3339: %v", err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("defaulted time %v not in window [%v, %v]", parsed, before, after)
	}
}

func TestRecord_OptionalFields_Omitted(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	// No Dimensions, no Resource.
	ev := validEvent()
	if err := rec.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var ce map[string]interface{}
	if err := json.Unmarshal(capturedBody, &ce); err != nil {
		t.Fatalf("parsing CE body: %v", err)
	}
	dataRaw := ce["data"]
	dataBytes, _ := json.Marshal(dataRaw)
	var data map[string]interface{}
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("parsing data: %v", err)
	}

	if _, present := data["dimensions"]; present {
		t.Error("dimensions should be omitted when nil/empty")
	}
	if _, present := data["resource"]; present {
		t.Error("resource should be omitted when nil")
	}
}

func TestRecord_IDReusedAcrossRetries(t *testing.T) {
	var receivedIDs []string
	var callCount int32

	// Fail twice, then succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		body, _ := io.ReadAll(r.Body)
		var ce map[string]interface{}
		_ = json.Unmarshal(body, &ce)
		if id, ok := ce["id"].(string); ok {
			receivedIDs = append(receivedIDs, id)
		}
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  5,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     5 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	if err := rec.Record(context.Background(), validEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if len(receivedIDs) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(receivedIDs))
	}
	// All attempts must share the same ULID.
	for i := 1; i < len(receivedIDs); i++ {
		if receivedIDs[i] != receivedIDs[0] {
			t.Errorf("attempt %d id %q != attempt 0 id %q", i, receivedIDs[i], receivedIDs[0])
		}
	}
}

// --- Retry behavior tests ---

func TestRecord_Success_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted) // 202 is also a 2xx
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	if err := rec.Record(context.Background(), validEvent()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRecord_Retry_5xx_ExhaustsRetries(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mp, reader := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  3,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     5 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	err = rec.Record(context.Background(), validEvent())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if int(atomic.LoadInt32(&callCount)) != 3 {
		t.Errorf("expected 3 attempts, got %d", callCount)
	}

	if count := collectCounterValue(t, reader, "billing_sdk_record_errors_total"); count != 1 {
		t.Errorf("billing_sdk_record_errors_total = %d, want 1", count)
	}
}

func TestRecord_Retry_429(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  3,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     5 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	err = rec.Record(context.Background(), validEvent())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if int(atomic.LoadInt32(&callCount)) != 3 {
		t.Errorf("expected 3 attempts, got %d", callCount)
	}
}

func TestRecord_DeadLetter_4xx_NoRetry(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	mp, reader := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  5,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     5 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	err = rec.Record(context.Background(), validEvent())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Must not retry on 4xx (not 429).
	if n := int(atomic.LoadInt32(&callCount)); n != 1 {
		t.Errorf("expected exactly 1 attempt (no retry), got %d", n)
	}

	if count := collectCounterValue(t, reader, "billing_sdk_dead_letter_total"); count != 1 {
		t.Errorf("billing_sdk_dead_letter_total = %d, want 1", count)
	}
	if count := collectCounterValue(t, reader, "billing_sdk_record_errors_total"); count != 1 {
		t.Errorf("billing_sdk_record_errors_total = %d, want 1", count)
	}
}

func TestRecord_4xx_Not429_IsDeadLetter_Not_404(t *testing.T) {
	// 404 is also a 4xx that should dead-letter without retry.
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	mp, _ := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  5,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     5 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	if err := rec.Record(context.Background(), validEvent()); err == nil {
		t.Fatal("expected error")
	}
	if n := int(atomic.LoadInt32(&callCount)); n != 1 {
		t.Errorf("expected 1 attempt, got %d", n)
	}
}

func TestRecord_ContextCancelledDuringSleep(t *testing.T) {
	callCount := int32(0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	mp, reader := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  5,
			BaseDelay:    500 * time.Millisecond, // long enough to cancel during sleep
			MaxDelay:     2 * time.Second,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err = rec.Record(ctx, validEvent())
	if err == nil {
		t.Fatal("expected error")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}

	// Should have made at least 1 attempt before sleeping, cancelled during backoff.
	if n := int(atomic.LoadInt32(&callCount)); n < 1 {
		t.Errorf("expected at least 1 attempt, got %d", n)
	}

	if count := collectCounterValue(t, reader, "billing_sdk_record_errors_total"); count != 1 {
		t.Errorf("billing_sdk_record_errors_total = %d, want 1", count)
	}
}

func TestRecord_MetricsRecordErrors_VectorUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mp, reader := newTestProvider()
	rec, err := emission.NewVectorRecorder(
		emission.WithVectorEndpoint(srv.URL),
		emission.WithMeterProvider(mp),
		emission.WithRetryPolicy(emission.RetryPolicy{
			MaxAttempts:  2,
			BaseDelay:    1 * time.Millisecond,
			MaxDelay:     2 * time.Millisecond,
			JitterFactor: 0,
		}),
	)
	if err != nil {
		t.Fatalf("NewVectorRecorder: %v", err)
	}

	rec.Record(context.Background(), validEvent()) //nolint:errcheck // error expected

	if count := collectCounterValue(t, reader, "billing_sdk_record_errors_total"); count != 1 {
		t.Errorf("billing_sdk_record_errors_total = %d, want 1", count)
	}
	// dead letter must NOT be incremented on 5xx.
	if count := collectCounterValue(t, reader, "billing_sdk_dead_letter_total"); count != 0 {
		t.Errorf("billing_sdk_dead_letter_total = %d, want 0", count)
	}
}

func TestRecord_NoopRecorder(t *testing.T) {
	var r emission.Recorder = emission.NoopRecorder{}
	if err := r.Record(context.Background(), validEvent()); err != nil {
		t.Errorf("NoopRecorder.Record: %v", err)
	}
}

// --- Helpers ---

// errorAs is a simple helper to avoid importing errors package in test.
func errorAs(err error, target **emission.ValidationError) bool {
	for err != nil {
		if ve, ok := err.(*emission.ValidationError); ok {
			*target = ve
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if uw, ok := err.(unwrapper); ok {
			err = uw.Unwrap()
		} else {
			break
		}
	}
	return false
}

func assertEqual(t *testing.T, field string, want, got interface{}) {
	t.Helper()
	wantStr := fmt.Sprintf("%v", want)
	gotStr := fmt.Sprintf("%v", got)
	if wantStr != gotStr {
		t.Errorf("%s: want %q, got %q", field, wantStr, gotStr)
	}
}
