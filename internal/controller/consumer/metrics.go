// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"context"
	"fmt"

	otelattribute "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// MetricValidationRejections is the OTel counter name for quarantine decisions.
	MetricValidationRejections = "billing_validation_rejections_total"

	// MetricMalformed is the OTel counter name for messages dropped before
	// validation because they could not be parsed or had an invalid structure.
	MetricMalformed = "billing_consumer_malformed_total"

	// LabelProject is the project label key.
	LabelProject = "project"

	// LabelReason is the quarantine reason label key.
	LabelReason = "reason"

	// Malformed reason values for billing_consumer_malformed_total.
	MalformedReasonUnmarshal      = "unmarshal_error"
	MalformedReasonInvalidSubject = "invalid_subject"
)

// consumerMetrics holds the OTel counters used by UsageConsumer.
type consumerMetrics struct {
	rejections metric.Int64Counter
	malformed  metric.Int64Counter
}

// registerMetrics registers all UsageConsumer OTel counters with the provided
// MeterProvider. Called once from UsageConsumer.Start before the event loop.
func registerMetrics(mp metric.MeterProvider) (consumerMetrics, error) {
	meter := mp.Meter("go.miloapis.com/billing/consumer")

	rejections, err := meter.Int64Counter(
		MetricValidationRejections,
		metric.WithDescription("Total usage events quarantined during central validation or attribution."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return consumerMetrics{}, fmt.Errorf("creating %s counter: %w", MetricValidationRejections, err)
	}

	malformed, err := meter.Int64Counter(
		MetricMalformed,
		metric.WithDescription("Total ingest messages dropped due to deserialization failure or invalid structure."),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return consumerMetrics{}, fmt.Errorf("creating %s counter: %w", MetricMalformed, err)
	}

	return consumerMetrics{rejections: rejections, malformed: malformed}, nil
}

// recordRejection increments billing_validation_rejections_total with the
// given project and reason labels.
func recordRejection(ctx context.Context, m consumerMetrics, project string, reason QuarantineReason) {
	m.rejections.Add(ctx, 1,
		metric.WithAttributes(
			otelattribute.String(LabelProject, project),
			otelattribute.String(LabelReason, string(reason)),
		),
	)
}

// recordMalformed increments billing_consumer_malformed_total with the given reason label.
func recordMalformed(ctx context.Context, m consumerMetrics, reason string) {
	m.malformed.Add(ctx, 1,
		metric.WithAttributes(
			otelattribute.String(LabelReason, reason),
		),
	)
}
