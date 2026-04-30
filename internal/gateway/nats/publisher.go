// SPDX-License-Identifier: AGPL-3.0-only

// Package nats provides NATS JetStream publishing for the ingestion gateway.
package nats

import (
	"context"
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("nats")

// Publisher abstracts JetStream event publishing.
// Injectable for testing (a fake can return nil, timeout, or connection error).
type Publisher interface {
	// Publish publishes a single raw CloudEvent JSON payload to the given
	// NATS subject. Returns an error if publish times out or NATS is unhealthy.
	Publish(ctx context.Context, subject string, payload []byte) error
}

// HealthChecker reports whether the underlying NATS connection is healthy.
type HealthChecker interface {
	Healthy() bool
}

// NATSPublisher implements Publisher and HealthChecker using a live NATS
// JetStream connection.
type NATSPublisher struct {
	nc             *natsgo.Conn
	js             jetstream.JetStream
	publishTimeout time.Duration
}

// NewNATSPublisher dials NATS and returns a NATSPublisher.
// Returns an error if the connection or JetStream context cannot be
// established — callers should treat this as a fatal startup error.
func NewNATSPublisher(url string) (*NATSPublisher, error) {
	nc, err := natsgo.Connect(url,
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, err error) {
			log.Error(err, "NATS disconnected")
		}),
		natsgo.ReconnectHandler(func(nc *natsgo.Conn) {
			log.Info("NATS reconnected", "url", nc.ConnectedUrl())
		}),
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
			log.Info("NATS connection closed")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("gateway: connecting to NATS at %s: %w", url, err)
	}
	log.Info("connected to NATS", "url", url)
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("gateway: creating JetStream context: %w", err)
	}
	return &NATSPublisher{
		nc:             nc,
		js:             js,
		publishTimeout: 500 * time.Millisecond,
	}, nil
}

// Publish publishes payload to subject using the JetStream PublishMsg API with
// a per-publish timeout. Returns an error on timeout or connection failure.
func (p *NATSPublisher) Publish(ctx context.Context, subject string, payload []byte) error {
	pubCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()
	msg := &natsgo.Msg{Subject: subject, Data: payload}
	_, err := p.js.PublishMsg(pubCtx, msg)
	return err
}

// Healthy reports whether the underlying NATS connection is currently connected.
func (p *NATSPublisher) Healthy() bool {
	return p.nc.IsConnected()
}
