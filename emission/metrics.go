// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"go.opentelemetry.io/otel/metric"
)

// sdkMetrics holds the two OTel counters emitted by UsageRecorder.
type sdkMetrics struct {
	recordErrors metric.Int64Counter // billing_sdk_record_errors_total
	rejected     metric.Int64Counter // billing_sdk_rejected_total
}

// newSDKMetrics registers both counters against the provided MeterProvider.
// Returns an error if registration fails (e.g., duplicate registration with
// incompatible configuration).
func newSDKMetrics(mp metric.MeterProvider) (*sdkMetrics, error) {
	meter := mp.Meter("go.miloapis.com/billing/emission")

	recordErrors, err := meter.Int64Counter(
		"billing_sdk_record_errors_total",
		metric.WithDescription("Total number of Record() calls that returned an error after exhausting the retry budget."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	rejected, err := meter.Int64Counter(
		"billing_sdk_rejected_total",
		metric.WithDescription("Total number of events permanently dropped by the endpoint (HTTP 4xx, not 429)."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, err
	}

	return &sdkMetrics{
		recordErrors: recordErrors,
		rejected:     rejected,
	}, nil
}
