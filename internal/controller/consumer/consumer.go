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

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/event"
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
	// Used for WaitForCacheSync.
	Cache cache.Cache

	// NC is the shared NATS connection.
	NC *natsgo.Conn

	// MeterCache is the watch-backed MeterDefinition index, keyed by spec.meterName.
	MeterCache *MeterDefinitionCache

	// BindingCache is the watch-backed Active BillingAccountBinding index,
	// keyed by spec.projectRef.name.
	BindingCache *BillingAccountBindingCache

	// AccountCache is the watch-backed Ready BillingAccount index,
	// keyed by metadata.name.
	AccountCache *BillingAccountCache

	// MeterProvider is the OTel MeterProvider used to register metrics.
	// When nil the consumer uses the noop provider (metrics are discarded).
	MeterProvider metric.MeterProvider

	// FetchBatch is the number of messages to fetch per pull request.
	// Defaults to ConsumerFetchBatch when zero.
	FetchBatch int

	// Logger is the structured logger for this consumer.
	Logger logr.Logger

	// DisableQuarantineOnAttributionFailure disables publishing quarantined
	// events to NATS when attribution fails (e.g. no billing account/binding exists).
	DisableQuarantineOnAttributionFailure bool

	// metrics holds the OTel counters for this consumer.
	metrics consumerMetrics
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
	metrics, err := registerMetrics(mp)
	if err != nil {
		return fmt.Errorf("consumer: registering metrics: %w", err)
	}
	c.metrics = metrics

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

	// Deserialize the CloudEvent payload.
	var ce cloudevents.Event
	if err := json.Unmarshal(msg.Data(), &ce); err != nil {
		log.Error(err, "failed to unmarshal event; dropping", "subject", msg.Subject())
		recordMalformed(ctx, c.metrics, MalformedReasonUnmarshal)
		return msg.Ack()
	}

	// Extract project from the CloudEvent subject: "projects/{project-name}".
	// The gateway validates and enforces this format before publishing to ingest.
	project := strings.TrimPrefix(ce.Subject(), "projects/")
	if project == ce.Subject() || project == "" {
		log.Error(fmt.Errorf("invalid CloudEvent subject %q", ce.Subject()), "dropping message",
			"subject", msg.Subject(),
		)
		recordMalformed(ctx, c.metrics, MalformedReasonInvalidSubject)
		return msg.Ack()
	}

	// Stage 1: Central Validation.
	vr := validate(&ce, c.MeterCache)
	if !vr.OK {
		return c.quarantine(ctx, js, msg, &ce, project, vr.Reason, vr.Detail)
	}

	// Inject project_id as a system dimension after validation so downstream
	// consumers can filter by project without it being declared on every
	// MeterDefinition.
	var eventData event.EventData
	_ = ce.DataAs(&eventData)
	if eventData.Dimensions == nil {
		eventData.Dimensions = make(map[string]string)
	}
	eventData.Dimensions[billingv1alpha1.SystemDimensionProjectName] = project
	if err := ce.SetData("application/json", eventData); err != nil {
		return fmt.Errorf("injecting project_id dimension: %w", err)
	}

	// Stage 2: Attribution.
	ar := attribute(project, c.BindingCache, c.AccountCache)
	if !ar.OK {
		if c.DisableQuarantineOnAttributionFailure && ar.Reason == ReasonAttributionFailure {
			recordRejection(ctx, c.metrics, project, ar.Reason)
			log.Info("event attribution failed; dropping event (quarantine disabled)",
				"project", project,
				"reason", ar.Reason,
				"detail", ar.Detail,
				"eventID", ce.ID(),
				"eventType", ce.Type(),
			)
			return msg.Ack()
		}
		return c.quarantine(ctx, js, msg, &ce, project, ar.Reason, ar.Detail)
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

	if _, err := js.Publish(pubCtx, validSubject, enriched, msgID(ce.ID(), "valid")); err != nil {
		return fmt.Errorf("publishing to %s: %w", validSubject, err)
	}

	log.V(1).Info("event attributed and published to valid",
		"project", project,
		"eventID", ce.ID(),
		"eventType", ce.Type(),
		"billingAccountRef", ar.BillingAccountRef,
		"value", eventData.Value,
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
	detail string,
) error {
	log := c.Logger.WithName("usage-consumer")

	quarantineSubject := fmt.Sprintf("billing.usage.%s.quarantine.%s", project, strings.ToLower(string(reason)))

	payload, err := json.Marshal(ce)
	if err != nil {
		return fmt.Errorf("marshaling quarantine event: %w", err)
	}

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if _, err := js.Publish(pubCtx, quarantineSubject, payload, msgID(ce.ID(), "quarantine")); err != nil {
		return fmt.Errorf("publishing to quarantine subject %s: %w", quarantineSubject, err)
	}

	recordRejection(ctx, c.metrics, project, reason)

	log.Info("event quarantined",
		"project", project,
		"reason", reason,
		"detail", detail,
		"eventID", ce.ID(),
		"eventType", ce.Type(),
		"subject", quarantineSubject,
	)

	return msg.Ack()
}

// msgID returns a PublishOpt that sets Nats-Msg-Id to the given CloudEvent ID with a suffix,
// enabling deduplication on downstream JetStream streams without colliding with other stream subjects.
func msgID(id, suffix string) jetstream.PublishOpt {
	return jetstream.WithMsgID(id + "-" + suffix)
}
