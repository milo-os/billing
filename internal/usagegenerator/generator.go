// SPDX-License-Identifier: AGPL-3.0-only

// Package usagegenerator provides a manager.Runnable that periodically emits
// synthetic usage events for demo and development environments. It must not
// be enabled in production.
package usagegenerator

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/emission"
)

const source = "//billing.miloapis.com/usage-generator"

var defaultMeters = []string{
	"compute.datumapis.com/instance/uptime-seconds",
}

// UsageGenerator is a manager.Runnable that ticks at a fixed interval and
// emits one usage event per meter per Active BillingAccountBinding project.
// Never enable in production — the events are fabricated and will inflate
// billing records.
type UsageGenerator struct {
	Client client.Client

	Recorder emission.Recorder

	Interval time.Duration

	Meters []string

	IncludedBindings []types.NamespacedName

	Logger logr.Logger
}

// Start implements manager.Runnable. It blocks until ctx is cancelled.
func (d *UsageGenerator) Start(ctx context.Context) error {
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

	d.Logger.Info("usage generator started", "interval", interval, "meters", meters, "bindings", len(d.IncludedBindings))

	for {
		select {
		case <-ctx.Done():
			d.Logger.Info("usage generator stopped")
			return nil
		case t := <-ticker.C:
			d.emit(ctx, t, interval, meters)
		}
	}
}

func (d *UsageGenerator) emit(ctx context.Context, occurredAt time.Time, interval time.Duration, meters []string) {
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
				d.Logger.Error(err, "recording usage event", "project", project, "meter", meter)
			} else {
				d.Logger.V(1).Info("emitted usage", "project", project, "meter", meter, "quantity", quantity)
			}
		}
	}
}
