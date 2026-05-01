// SPDX-License-Identifier: AGPL-3.0-only

// Package consumer implements the NATS JetStream pull consumer that applies
// Central Validation and Attribution to ingest events.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

const (
	// ConsumerDurableName is the durable pull consumer name. JetStream persists
	// the consumer ack sequence under this name so no events are lost on restart.
	ConsumerDurableName = "billing-usage-validator"

	// ConsumerFetchBatch is the default number of messages to fetch per pull.
	ConsumerFetchBatch = 100

	// ConsumerFetchTimeout is the maximum wait for a pull fetch when the stream
	// is empty. Prevents a busy-loop when there are no pending messages.
	ConsumerFetchTimeout = 5 * time.Second

	// IngestSubjectFilter covers all project ingest subjects via wildcard.
	// The consumer extracts the project name from msg.Subject at index 2
	// (billing.usage.{project}.ingest → split on "." → [billing usage {project} ingest]).
	IngestSubjectFilter = "billing.usage.*.ingest"

	// publishTimeout is the per-publish context deadline, consistent with the
	// feat-002 gateway pattern.
	publishTimeout = 500 * time.Millisecond
)

// UsageConsumer is a manager.Runnable that consumes events from the
// billing.usage.{project}.ingest NATS JetStream subjects, applies Central
// Validation and Attribution, and routes events to the valid or quarantine
// subjects.
type UsageConsumer struct {
	// Cache is the controller-runtime cache, shared with the reconcilers.
	// Used for WaitForCacheSync and BillingAccount lookups in attribution.
	Cache cache.Cache

	// NC is the shared NATS connection.
	NC *natsgo.Conn

	// MeterCache is the watch-backed MeterDefinition index, keyed by spec.meterName.
	MeterCache *MeterDefinitionCache

	// BindingCache is the watch-backed Active BillingAccountBinding index,
	// keyed by spec.projectRef.name.
	BindingCache *BillingAccountBindingCache

	// MeterProvider is the OTel MeterProvider used to register metrics.
	// When nil the consumer uses the noop provider (metrics are discarded).
	MeterProvider metric.MeterProvider

	// FetchBatch is the number of messages to fetch per pull request.
	// Defaults to ConsumerFetchBatch when zero.
	FetchBatch int

	// Logger is the structured logger for this consumer.
	Logger logr.Logger

	// rejections is the OTel Counter for validation/attribution rejections.
	rejections metric.Int64Counter
}

// Start implements manager.Runnable. It is called by the manager after leader
// election (if enabled). It blocks until ctx is cancelled.
func (c *UsageConsumer) Start(ctx context.Context) error {
	log := c.Logger.WithName("usage-consumer")

	fetchBatch := c.FetchBatch
	if fetchBatch <= 0 {
		fetchBatch = ConsumerFetchBatch
	}

	// Register OTel metrics. Fall back to noop when no provider is set.
	mp := c.MeterProvider
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	counter, err := registerMetrics(mp)
	if err != nil {
		return fmt.Errorf("consumer: registering metrics: %w", err)
	}
	c.rejections = counter

	// Wait for the informer cache to sync before processing any events.
	// This prevents false UNKNOWN_METER quarantine due to an unsynced cache.
	log.Info("waiting for cache sync")
	if !c.Cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("consumer: cache sync timed out or context cancelled")
	}
	log.Info("cache synced; starting event loop")

	// Create JetStream context.
	js, err := jetstream.New(c.NC)
	if err != nil {
		return fmt.Errorf("consumer: creating JetStream context: %w", err)
	}

	// Create or bind the durable pull consumer.
	cons, err := js.CreateOrUpdateConsumer(ctx, "billing-usage", jetstream.ConsumerConfig{
		Durable:       ConsumerDurableName,
		FilterSubject: IngestSubjectFilter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: -1,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("consumer: creating JetStream consumer: %w", err)
	}

	log.Info("pull consumer ready",
		"durable", ConsumerDurableName,
		"filterSubject", IngestSubjectFilter,
		"fetchBatch", fetchBatch,
	)

	for {
		// Check context before each fetch.
		select {
		case <-ctx.Done():
			log.Info("context cancelled; stopping consumer")
			return nil
		default:
		}

		msgs, err := cons.Fetch(fetchBatch, jetstream.FetchMaxWait(ConsumerFetchTimeout))
		if err != nil {
			// FetchMaxWait returns nats.ErrTimeout when the stream is empty —
			// this is normal. Any other error is unexpected.
			if strings.Contains(err.Error(), "timeout") {
				continue
			}
			log.Error(err, "fetch error; retrying after backoff")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Second):
			}
			continue
		}

		for msg := range msgs.Messages() {
			if err := c.processMessage(ctx, js, msg); err != nil {
				log.Error(err, "failed to process message; nacking for redelivery",
					"subject", msg.Subject(),
				)
				_ = msg.Nak()
			}
		}

		if err := msgs.Error(); err != nil {
			log.Error(err, "message batch error")
		}
	}
}

// processMessage applies validation, attribution, and routing for a single
// ingest message.
func (c *UsageConsumer) processMessage(
	ctx context.Context,
	js jetstream.JetStream,
	msg jetstream.Msg,
) error {
	log := c.Logger.WithName("usage-consumer")

	// Extract project name from subject: billing.usage.{project}.ingest
	parts := strings.Split(msg.Subject(), ".")
	if len(parts) != 4 {
		// Malformed subject — quarantine is not possible without a project name.
		// Ack to prevent infinite redelivery.
		log.Error(fmt.Errorf("unexpected subject format"), "dropping malformed message",
			"subject", msg.Subject(),
		)
		return msg.Ack()
	}
	project := parts[2]

	// Deserialize the CloudEvent payload.
	var ce cloudevents.Event
	if err := json.Unmarshal(msg.Data(), &ce); err != nil {
		log.Error(err, "failed to unmarshal event; dropping",
			"project", project,
			"subject", msg.Subject(),
		)
		return msg.Ack()
	}

	// Stage 1: Central Validation.
	vr := validate(&ce, c.MeterCache)
	if !vr.OK {
		return c.quarantine(ctx, js, msg, &ce, project, vr.Reason)
	}

	// Stage 2: Attribution.
	ar, err := attribute(ctx, project, c.BindingCache, c.Cache)
	if err != nil {
		return fmt.Errorf("attribute: %w", err)
	}
	if !ar.OK {
		return c.quarantine(ctx, js, msg, &ce, project, ar.Reason)
	}

	// Enrich event with billing account reference as a CloudEvents extension.
	ce.SetExtension("billingaccountref", ar.BillingAccountRef)

	// Publish to valid subject.
	enriched, err := json.Marshal(&ce)
	if err != nil {
		return fmt.Errorf("marshaling enriched event: %w", err)
	}

	validSubject := fmt.Sprintf("billing.usage.%s.valid", project)
	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if _, err := js.Publish(pubCtx, validSubject, enriched); err != nil {
		return fmt.Errorf("publishing to %s: %w", validSubject, err)
	}

	log.V(1).Info("event attributed and published to valid",
		"project", project,
		"eventID", ce.ID(),
		"billingAccountRef", ar.BillingAccountRef,
	)

	return msg.Ack()
}

// quarantine publishes an event to the per-reason quarantine subject, increments
// the rejection counter, and acks the original ingest message.
func (c *UsageConsumer) quarantine(
	ctx context.Context,
	js jetstream.JetStream,
	msg jetstream.Msg,
	ce *cloudevents.Event,
	project string,
	reason QuarantineReason,
) error {
	log := c.Logger.WithName("usage-consumer")

	quarantineSubject := fmt.Sprintf("billing.usage.%s.quarantine.%s", project, strings.ToLower(string(reason)))

	payload, err := json.Marshal(ce)
	if err != nil {
		return fmt.Errorf("marshaling quarantine event: %w", err)
	}

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if _, err := js.Publish(pubCtx, quarantineSubject, payload); err != nil {
		return fmt.Errorf("publishing to quarantine subject %s: %w", quarantineSubject, err)
	}

	recordRejection(ctx, c.rejections, project, reason)

	log.Info("event quarantined",
		"project", project,
		"reason", reason,
		"eventID", ce.ID(),
		"eventType", ce.Type(),
		"subject", quarantineSubject,
	)

	return msg.Ack()
}
