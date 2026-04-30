// SPDX-License-Identifier: AGPL-3.0-only

package nats_test

import (
	"context"
	"errors"
	"testing"

	"go.miloapis.com/billing/internal/gateway/nats"
)

// fakePublisher is a Publisher for use in unit tests.
type fakePublisher struct {
	err     error
	healthy bool
}

var _ nats.Publisher = (*fakePublisher)(nil)
var _ nats.HealthChecker = (*fakePublisher)(nil)

func (f *fakePublisher) Publish(_ context.Context, _ string, _ []byte) error {
	return f.err
}

func (f *fakePublisher) Healthy() bool {
	return f.healthy
}

func TestFakePublisher_ok(t *testing.T) {
	p := &fakePublisher{err: nil, healthy: true}
	if err := p.Publish(context.Background(), "test.subject", []byte("{}")); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !p.Healthy() {
		t.Fatal("expected healthy")
	}
}

func TestFakePublisher_error(t *testing.T) {
	want := errors.New("nats: timeout")
	p := &fakePublisher{err: want, healthy: false}
	err := p.Publish(context.Background(), "test.subject", []byte("{}"))
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}
