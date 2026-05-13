// SPDX-License-Identifier: AGPL-3.0-only

// Package fakeusage provides a manager.Runnable that periodically emits
// synthetic usage events for demo and development environments. It must not
// be enabled in production.
package fakeusage

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/emission"
)

const source = "//billing.miloapis.com/fake-usage-daemon"

// defaultMeters are the meters emitted when the Meters field is empty.
var defaultMeters = []string{
	"compute.datumapis.com/instance/uptime-seconds",
}

// FakeUsageDaemon is a manager.Runnable that ticks at a fixed interval and
// emits one usage event per meter per Active BillingAccountBinding project.
//
// Enable via --fake-usage-endpoint on the operator command. Never enable in
// production — the events are fabricated and will inflate billing records.
type FakeUsageDaemon struct {
	// Client is the controller-runtime client (cache-backed).
	Client client.Client

	// Recorder forwards events to the usage collector.
	Recorder emission.Recorder

	// Interval between emission ticks. Defaults to 30s when zero.
	Interval time.Duration

	// Meters is the list of meter names to emit on each tick. Defaults to
	// defaultMeters when nil or empty.
	Meters []string

	// IncludedBindings is the set of BillingAccountBindings to emit usage for.
	// Each binding is read from the cache on every tick; bindings not in the
	// Active phase are skipped. When empty, no usage is emitted.
	IncludedBindings []types.NamespacedName

	Logger logr.Logger
}

// Start implements manager.Runnable. It blocks until ctx is cancelled.
func (d *FakeUsageDaemon) Start(ctx context.Context) error {
	interval := d.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	meters := d.Meters
	if len(meters) == 0 {
		meters = defaultMeters
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	d.Logger.Info("fake usage daemon started", "interval", interval, "meters", meters, "bindings", len(d.IncludedBindings))

	for {
		select {
		case <-ctx.Done():
			d.Logger.Info("fake usage daemon stopped")
			return nil
		case t := <-ticker.C:
			d.emit(ctx, t, interval, meters)
		}
	}
}

// emit reads each binding in IncludedBindings from the cache and records one
// event per meter for each Active binding's project.
func (d *FakeUsageDaemon) emit(ctx context.Context, occurredAt time.Time, interval time.Duration, meters []string) {
	// quantity is the number of seconds in the tick interval so that
	// uptime-seconds meters accumulate at wall-clock rate.
	quantity := int64(interval.Seconds())
	if quantity < 1 {
		quantity = 1
	}

	for _, ref := range d.IncludedBindings {
		var b billingv1alpha1.BillingAccountBinding
		if err := d.Client.Get(ctx, ref, &b); err != nil {
			d.Logger.Error(err, "getting BillingAccountBinding", "binding", ref)
			continue
		}
		if b.Status.Phase != billingv1alpha1.BillingAccountBindingPhaseActive {
			continue
		}

		project := b.Spec.ProjectRef.Name
		for _, meter := range meters {
			ev := emission.UsageEvent{
				Meter:      meter,
				Project:    emission.ProjectRef{Name: project},
				Source:     source,
				Quantity:   quantity,
				OccurredAt: occurredAt,
			}
			if err := d.Recorder.Record(ctx, ev); err != nil {
				d.Logger.Error(err, "recording fake usage event", "project", project, "meter", meter)
			} else {
				d.Logger.V(1).Info("emitted fake usage", "project", project, "meter", meter, "quantity", quantity)
			}
		}
	}
}
