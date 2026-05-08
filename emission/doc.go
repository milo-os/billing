// SPDX-License-Identifier: AGPL-3.0-only

// Package emission provides a typed Go SDK for recording billable usage events.
//
// Service teams import this package to emit usage events to the platform's
// billing pipeline via the usage endpoint. Callers should not construct
// CloudEvents directly or interact with the endpoint; this package is the
// sole sanctioned entry point.
//
// Basic usage:
//
//	recorder, err := emission.NewUsageRecorder()
//	if err != nil {
//	    return fmt.Errorf("creating recorder: %w", err)
//	}
//	err = recorder.Record(ctx, emission.UsageEvent{
//	    Meter:      "compute.miloapis.com/instance/cpu-seconds",
//	    Project:    emission.ProjectRef{Name: "p-abc"},
//	    Source:     "//compute.miloapis.com/controllers/instance-reconciler",
//	    Quantity:   42,
//	    OccurredAt: time.Now(),
//	})
package emission
