// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// Option configures a UsageRecorder. Options are applied in order at
// construction time via NewUsageRecorder.
type Option func(*recorderConfig)

// RetryPolicy controls the bounded exponential backoff applied by
// UsageRecorder when the endpoint returns a transient error.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (1 initial + N-1 retries).
	// Must be >= 1. Default: 5.
	MaxAttempts int

	// BaseDelay is the delay before the first retry. Default: 100ms.
	BaseDelay time.Duration

	// MaxDelay caps the computed backoff. Default: 2s.
	MaxDelay time.Duration

	// JitterFactor is the fraction of the computed delay added or subtracted
	// as random jitter (0.25 = ±25%). Default: 0.25.
	JitterFactor float64
}

// defaultRetryPolicy is the out-of-the-box policy. Worst-case added latency
// before Record() returns an error: approximately 4 s.
var defaultRetryPolicy = RetryPolicy{
	MaxAttempts:  5,
	BaseDelay:    100 * time.Millisecond,
	MaxDelay:     2 * time.Second,
	JitterFactor: 0.25,
}

// recorderConfig holds the resolved configuration for a UsageRecorder.
// It is unexported; callers must use Option functions to set values.
type recorderConfig struct {
	endpoint      string
	retryPolicy   RetryPolicy
	meterProvider metric.MeterProvider
	httpClient    *http.Client
}

func defaultRecorderConfig() recorderConfig {
	return recorderConfig{
		endpoint:      "http://localhost:9880/cloudevents",
		retryPolicy:   defaultRetryPolicy,
		meterProvider: nil, // falls back to otel.GetMeterProvider()
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// WithEndpoint overrides the default usage endpoint URL.
//
// The default is http://localhost:9880/cloudevents, which matches the
// DaemonSet configuration shipped in feat-002. Override this in environments
// where the port or path differs.
func WithEndpoint(addr string) Option {
	return func(c *recorderConfig) {
		c.endpoint = addr
	}
}

// WithRetryPolicy overrides the default retry policy.
//
// The default policy (5 attempts, 100ms base, 2s max, ±25% jitter) is
// designed for controller reconcile loops. Override when a tighter or
// looser budget is required.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *recorderConfig) {
		c.retryPolicy = p
	}
}

// WithHTTPClient overrides the HTTP client used to POST events to the usage
// endpoint. Use this to inject a custom transport, set timeouts, or provide a
// test double without starting a real HTTP server.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *recorderConfig) {
		cfg.httpClient = c
	}
}

// WithMeterProvider sets the OTel MeterProvider used for SDK metrics.
//
// When not supplied, UsageRecorder falls back to the global OTel provider
// (otel.GetMeterProvider()). Inject a custom provider in tests to assert
// counter increments without a live Prometheus exporter.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *recorderConfig) {
		c.meterProvider = mp
	}
}
