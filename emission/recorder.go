// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// lockedRand wraps math/rand.Rand with a mutex to implement io.Reader in a
// goroutine-safe manner, satisfying oklog/ulid's entropy interface.
type lockedRand struct {
	mu  sync.Mutex
	src *rand.Rand
}

func (lr *lockedRand) Read(p []byte) (int, error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return lr.src.Read(p) //nolint:staticcheck // math/rand.Rand.Read is sufficient for ULID entropy
}

// VectorRecorder implements Recorder by forwarding events to the node-local
// Vector Agent over HTTP with bounded exponential backoff retry.
//
// VectorRecorder is safe for concurrent use from multiple goroutines. The
// HTTP client and entropy source are shared; all per-call state is
// stack-allocated.
type VectorRecorder struct {
	endpoint string
	policy   RetryPolicy
	client   *http.Client
	entropy  io.Reader // *lockedRand wrapping math/rand.New
	metrics  *sdkMetrics
}

// NewVectorRecorder creates a Recorder that forwards events to the
// node-local Vector Agent.
//
// NewVectorRecorder returns an error if counter registration with the
// configured MeterProvider fails. In practice this only occurs when the
// provider itself is misconfigured.
//
// Example:
//
//	r, err := emission.NewVectorRecorder(
//	    emission.WithVectorEndpoint("http://localhost:9880/cloudevents"),
//	)
func NewVectorRecorder(opts ...Option) (*VectorRecorder, error) {
	cfg := defaultRecorderConfig()
	for _, o := range opts {
		o(&cfg)
	}

	mp := cfg.meterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}

	m, err := newSDKMetrics(mp)
	if err != nil {
		return nil, fmt.Errorf("emission: registering metrics: %w", err)
	}

	src := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // non-crypto use: ULID entropy

	return &VectorRecorder{
		endpoint: cfg.vectorEndpoint,
		policy:   cfg.retryPolicy,
		client:   cfg.httpClient,
		entropy:  &lockedRand{src: src},
		metrics:  m,
	}, nil
}

// Record validates the event, wraps it in a CloudEvents envelope, and POSTs
// it to the Vector Agent. The same ULID is reused across retry attempts to
// allow downstream deduplication.
func (r *VectorRecorder) Record(ctx context.Context, ev UsageEvent) error {
	if err := validate(ev); err != nil {
		return err
	}

	now := time.Now()

	// Generate the ULID once; all retry attempts share the same id so that
	// downstream stages can deduplicate retried deliveries.
	id := ulid.MustNew(ulid.Timestamp(now), r.entropy).String()

	for attempt := 1; ; attempt++ {
		ce, err := toCloudEvent(ev, now, id)
		if err != nil {
			return fmt.Errorf("emission: building CloudEvent: %w", err)
		}

		payload, err := json.Marshal(ce)
		if err != nil {
			return fmt.Errorf("emission: marshalling CloudEvent: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("emission: building request: %w", err)
		}
		req.Header.Set("Content-Type", "application/cloudevents+json")

		resp, doErr := r.client.Do(req)

		// Context cancelled — surface immediately without consuming a retry slot.
		if ctx.Err() != nil {
			r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "context_canceled")))
			return ctx.Err()
		}

		if doErr != nil {
			// Connection-level error; treat as transient.
			if attempt >= r.policy.MaxAttempts {
				r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "vector_unavailable")))
				return fmt.Errorf("emission: vector unavailable after %d attempts: %w", attempt, doErr)
			}
			if err := r.sleep(ctx, attempt); err != nil {
				r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "context_canceled")))
				return err
			}
			continue
		}

		statusCode := resp.StatusCode
		_ = resp.Body.Close()

		switch {
		case statusCode >= 200 && statusCode < 300:
			return nil

		case statusCode == 429 || statusCode >= 500:
			if attempt >= r.policy.MaxAttempts {
				r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "vector_unavailable")))
				return fmt.Errorf("emission: vector returned %d after %d attempts", statusCode, attempt)
			}
			if err := r.sleep(ctx, attempt); err != nil {
				r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "context_canceled")))
				return err
			}

		default:
			// 4xx (not 429): permanent rejection, dead letter.
			r.metrics.deadLetter.Add(ctx, 1)
			r.metrics.recordErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "dead_letter")))
			return fmt.Errorf("emission: vector permanently rejected event: HTTP %d", statusCode)
		}
	}
}

// sleep waits for the backoff duration computed from the attempt number,
// returning ctx.Err() if the context is cancelled during the wait.
func (r *VectorRecorder) sleep(ctx context.Context, attempt int) error {
	delay := computeBackoff(r.policy, attempt, r.entropy)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// computeBackoff calculates the jittered backoff duration for the given
// attempt number according to the policy.
func computeBackoff(p RetryPolicy, attempt int, rng io.Reader) time.Duration {
	// delay = min(BaseDelay * 2^(attempt-1), MaxDelay)
	exp := math.Pow(2, float64(attempt-1))
	delay := min(time.Duration(float64(p.BaseDelay)*exp), p.MaxDelay)

	// jitter = delay * JitterFactor * (rand in [-1, 1])
	var buf [8]byte
	_, _ = rng.Read(buf[:])
	// Reconstruct a float64 in [0,1) from the random bytes.
	u64 := uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56
	// Map to [0.0, 1.0)
	f := float64(u64>>11) / float64(1<<53)
	jitter := time.Duration(float64(delay) * p.JitterFactor * (f*2 - 1))

	return delay + jitter
}
