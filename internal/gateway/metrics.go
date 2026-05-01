// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// gatewayMetrics holds the OTel counters for the Gateway.
type gatewayMetrics struct {
	eventsTotal     metric.Int64Counter
	rejectionsTotal metric.Int64Counter
}

// newGatewayMetrics registers the Gateway OTel counters against mp.
func newGatewayMetrics(mp metric.MeterProvider) (*gatewayMetrics, error) {
	meter := mp.Meter("go.miloapis.com/billing/gateway")

	eventsTotal, err := meter.Int64Counter(
		"billing_ingestion_events_total",
		metric.WithDescription("Total usage events accepted and durably committed to JetStream."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating billing_ingestion_events_total counter: %w", err)
	}

	rejectionsTotal, err := meter.Int64Counter(
		"billing_ingestion_rejections_total",
		metric.WithDescription("Total usage events rejected during structural validation."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating billing_ingestion_rejections_total counter: %w", err)
	}

	return &gatewayMetrics{
		eventsTotal:     eventsTotal,
		rejectionsTotal: rejectionsTotal,
	}, nil
}

// RecordAccepted increments billing_ingestion_events_total for the given project.
func (m *gatewayMetrics) RecordAccepted(ctx context.Context, project string) {
	m.eventsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("project", project),
			attribute.String("status", "accepted"),
		),
	)
}

// RecordRejected increments billing_ingestion_rejections_total for the given
// project and rejection reason.
func (m *gatewayMetrics) RecordRejected(ctx context.Context, project, reason string) {
	m.rejectionsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("project", project),
			attribute.String("reason", reason),
		),
	)
}
