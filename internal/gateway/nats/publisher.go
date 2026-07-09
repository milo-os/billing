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
	Publish(ctx context.Context, subject string, payload []byte, msgID string) error
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
func NewNATSPublisher(url, caFile, certFile, keyFile string) (*NATSPublisher, error) {
	opts := []natsgo.Option{
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, err error) {
			log.Error(err, "NATS disconnected")
		}),
		natsgo.ReconnectHandler(func(nc *natsgo.Conn) {
			log.Info("NATS reconnected", "url", nc.ConnectedUrl())
		}),
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
			log.Info("NATS connection closed")
		}),
	}

	if caFile != "" && certFile != "" && keyFile != "" {
		opts = append(opts, natsgo.RootCAs(caFile))
		opts = append(opts, natsgo.ClientCert(certFile, keyFile))
	}

	nc, err := natsgo.Connect(url, opts...)
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

// Publish publishes payload to subject using the JetStream Publish API with
// a per-publish timeout. Returns an error on timeout or connection failure.
func (p *NATSPublisher) Publish(ctx context.Context, subject string, payload []byte, msgID string) error {
	pubCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()

	var opts []jetstream.PublishOpt
	if msgID != "" {
		opts = append(opts, jetstream.WithMsgID(msgID))
	}

	_, err := p.js.Publish(pubCtx, subject, payload, opts...)
	return err
}

// Healthy reports whether the underlying NATS connection is currently connected.
func (p *NATSPublisher) Healthy() bool {
	return p.nc.IsConnected()
}
