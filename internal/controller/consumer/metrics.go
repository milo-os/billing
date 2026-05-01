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

	// LabelProject is the project label key.
	LabelProject = "project"

	// LabelReason is the quarantine reason label key.
	LabelReason = "reason"
)

// registerMetrics registers the billing_validation_rejections_total counter
// with the provided OTel MeterProvider and returns the counter.
// Called once from UsageConsumer.Start before the event loop.
func registerMetrics(mp metric.MeterProvider) (metric.Int64Counter, error) {
	meter := mp.Meter("go.miloapis.com/billing/consumer")

	counter, err := meter.Int64Counter(
		MetricValidationRejections,
		metric.WithDescription("Total usage events quarantined during central validation or attribution."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating %s counter: %w", MetricValidationRejections, err)
	}

	return counter, nil
}

// recordRejection increments billing_validation_rejections_total with the
// given project and reason labels.
func recordRejection(ctx context.Context, counter metric.Int64Counter, project string, reason QuarantineReason) {
	counter.Add(ctx, 1,
		metric.WithAttributes(
			otelattribute.String(LabelProject, project),
			otelattribute.String(LabelReason, string(reason)),
		),
	)
}
