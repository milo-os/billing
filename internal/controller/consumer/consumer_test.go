// SPDX-License-Identifier: AGPL-3.0-only

package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	"go.miloapis.com/billing/internal/event"
)

// --------------------------------------------------------------------------
// Embedded NATS server helpers
// --------------------------------------------------------------------------

// startTestNATSServer starts an embedded NATS server with JetStream enabled on
// a random port. Both the server and the client connection are shut down via
// t.Cleanup.
func startTestNATSServer(t *testing.T) (*natsserver.Server, *natsgo.Conn) {
	t.Helper()

	opts := &natsserver.Options{
		Port:           -1, // random available port
		JetStream:      true,
		StoreDir:       t.TempDir(),
		NoLog:          true,
		NoSigs:         true,
		MaxControlLine: 4096,
	}

	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("starting embedded NATS server: %v", err)
	}
	srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not become ready in time")
	}

	nc, err := natsgo.Connect(srv.ClientURL())
	if err != nil {
		srv.Shutdown()
		t.Fatalf("connecting to embedded NATS: %v", err)
	}

	t.Cleanup(func() {
		nc.Close()
		srv.Shutdown()
	})

	return srv, nc
}

// createBillingUsageStream creates the billing.usage.> JetStream stream.
// Mirrors the stream provisioned by feat-002.
func createBillingUsageStream(t *testing.T, nc *natsgo.Conn) {
	t.Helper()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("creating JetStream context: %v", err)
	}
	_, err = js.AddStream(&natsgo.StreamConfig{
		Name:     "billing-usage",
		Subjects: []string{"billing.usage.>"},
		Storage:  natsgo.MemoryStorage,
	})
	if err != nil {
		t.Fatalf("creating billing-usage stream: %v", err)
	}
}

// --------------------------------------------------------------------------
// Fake cache.Cache implementation
//
// UsageConsumer.Start uses two methods from cache.Cache:
//  1. WaitForCacheSync(ctx) — to block until the informer is ready.
//  2. Get (via client.Reader) — passed to attribute() so it can look up
//     BillingAccount objects.
//
// All other cache.Cache / cache.Informers methods are stubbed as no-ops.
// --------------------------------------------------------------------------

// fakeCache satisfies cache.Cache for consumer tests.
// synced controls the WaitForCacheSync return value.
// reader is the client.Reader used for Get calls (attribute step).
type fakeCache struct {
	synced bool
	reader client.Reader
}

var _ cache.Cache = (*fakeCache)(nil)

// client.Reader
func (c *fakeCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.reader.Get(ctx, key, obj, opts...)
}
func (c *fakeCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.reader.List(ctx, list, opts...)
}

// cache.Informers
func (c *fakeCache) GetInformer(_ context.Context, _ client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, nil
}
func (c *fakeCache) GetInformerForKind(_ context.Context, _ schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, nil
}
func (c *fakeCache) RemoveInformer(_ context.Context, _ client.Object) error { return nil }
func (c *fakeCache) Start(_ context.Context) error                           { return nil }
func (c *fakeCache) WaitForCacheSync(_ context.Context) bool                 { return c.synced }
func (c *fakeCache) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return nil
}

// newFakeCache returns a fakeCache backed by a controller-runtime fake client
// pre-populated with the supplied BillingAccount objects.
func newFakeCache(synced bool, accounts ...billingv1alpha1.BillingAccount) *fakeCache {
	b := fake.NewClientBuilder().WithScheme(newTestScheme())
	for i := range accounts {
		b = b.WithObjects(&accounts[i])
	}
	return &fakeCache{synced: synced, reader: b.Build()}
}

// --------------------------------------------------------------------------
// NATS publish / subscribe helpers
// --------------------------------------------------------------------------

// mustPublishIngest publishes a CloudEvent to the ingest subject for the
// project and blocks until JetStream acknowledges the write.
func mustPublishIngest(t *testing.T, nc *natsgo.Conn, project string, ce cloudevents.Event) {
	t.Helper()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("creating JetStream context: %v", err)
	}
	payload, err := json.Marshal(ce)
	if err != nil {
		t.Fatalf("marshaling event: %v", err)
	}
	subject := fmt.Sprintf("billing.usage.%s.ingest", project)
	if _, err := js.Publish(subject, payload); err != nil {
		t.Fatalf("publishing to %s: %v", subject, err)
	}
}

// subscribeSubject subscribes to a NATS subject and returns a buffered channel
// that receives all incoming messages.
func subscribeSubject(t *testing.T, nc *natsgo.Conn, subject string) chan *natsgo.Msg {
	t.Helper()
	ch := make(chan *natsgo.Msg, 8)
	sub, err := nc.Subscribe(subject, func(m *natsgo.Msg) { ch <- m })
	if err != nil {
		t.Fatalf("subscribing to %s: %v", subject, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return ch
}

// --------------------------------------------------------------------------
// Consumer construction helper
// --------------------------------------------------------------------------

func buildConsumer(
	t *testing.T,
	nc *natsgo.Conn,
	fc *fakeCache,
	mc *MeterDefinitionCache,
	bc *BillingAccountBindingCache,
) *UsageConsumer {
	t.Helper()
	return &UsageConsumer{
		Cache:         fc,
		NC:            nc,
		MeterCache:    mc,
		BindingCache:  bc,
		MeterProvider: noop.NewMeterProvider(),
		Logger:        logr.Discard(),
	}
}

// --------------------------------------------------------------------------
// Counting MeterProvider
//
// Intercepts Int64Counter.Add to count rejection metric calls without
// requiring a full Prometheus SDK pipeline.
// --------------------------------------------------------------------------

type atomicCounter struct{ n atomic.Int64 }

func (c *atomicCounter) Inc()        { c.n.Add(1) }
func (c *atomicCounter) Load() int64 { return c.n.Load() }

type countingMeterProvider struct {
	noop.MeterProvider
	counter *atomicCounter
}

func newCountingMeterProvider() (*countingMeterProvider, *atomicCounter) {
	ctr := &atomicCounter{}
	return &countingMeterProvider{counter: ctr}, ctr
}

func (mp *countingMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return &countingMeter{counter: mp.counter}
}

type countingMeter struct {
	noop.Meter
	counter *atomicCounter
}

func (m *countingMeter) Int64Counter(_ string, _ ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return &countingInt64Counter{counter: m.counter}, nil
}

type countingInt64Counter struct {
	noop.Int64Counter
	counter *atomicCounter
}

func (c *countingInt64Counter) Add(_ context.Context, incr int64, _ ...metric.AddOption) {
	c.counter.n.Add(incr)
}

// --------------------------------------------------------------------------
// Compile-time interface check
// --------------------------------------------------------------------------

var (
	_ cache.Cache          = (*fakeCache)(nil)
	_ metric.MeterProvider = (*countingMeterProvider)(nil)
	_ metric.Meter         = (*countingMeter)(nil)
	_ metric.Int64Counter  = (*countingInt64Counter)(nil)
)

// Suppress unused import; runtime is needed by the scheme helper in
// attribute_test.go (same package).
var _ = runtime.NewScheme

// --------------------------------------------------------------------------
// Unit test: WaitForCacheSync failure returns an error.
// --------------------------------------------------------------------------

func TestUsageConsumer_CacheSyncFailure_ReturnsError(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	fc := newFakeCache(false /* never syncs */)
	c := buildConsumer(t, nc, fc, newTestMeterCache(), newTestBindingCache())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := c.Start(ctx)
	if err == nil {
		t.Error("expected non-nil error when cache sync fails, got nil")
	}
}

// --------------------------------------------------------------------------
// Unit test: graceful shutdown on context cancellation.
// --------------------------------------------------------------------------

func TestUsageConsumer_GracefulShutdown(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	fc := newFakeCache(true)
	c := buildConsumer(t, nc, fc, newTestMeterCache(), newTestBindingCache())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error on graceful shutdown, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not shut down within 5 seconds after context cancellation")
	}
}

// --------------------------------------------------------------------------
// Integration: happy path — valid event published to billing.usage.{p}.valid.
// --------------------------------------------------------------------------

func TestUsageConsumer_HappyPath_PublishesToValid(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region"})
	binding := activeBinding("binding-1", "proj-abc", "acct-1")
	account := readyAccount("acct-1")

	fc := newFakeCache(true, account)
	c := buildConsumer(t, nc, fc, newTestMeterCache(md), newTestBindingCache(binding))

	validCh := subscribeSubject(t, nc, "billing.usage.proj-abc.valid")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/cpu-seconds")
	ce.SetSource("/test")
	_ = ce.SetData("application/json", event.EventData{Dimensions: map[string]string{"region": "us-east-1"}})
	mustPublishIngest(t, nc, "proj-abc", ce)

	select {
	case msg := <-validCh:
		var enriched cloudevents.Event
		if err := json.Unmarshal(msg.Data, &enriched); err != nil {
			t.Fatalf("unmarshaling valid event: %v", err)
		}
		ext := enriched.Extensions()
		billingRef, _ := ext["billingaccountref"].(string)
		if billingRef != "acct-1" {
			t.Errorf("expected billingaccountref=acct-1, got %q", billingRef)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("valid event not received within 10 seconds")
	}
}

// --------------------------------------------------------------------------
// Integration: UNKNOWN_METER quarantine path.
// --------------------------------------------------------------------------

func TestUsageConsumer_UnknownMeter_QuarantinesCorrectly(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	fc := newFakeCache(true)
	c := buildConsumer(t, nc, fc, newTestMeterCache(), newTestBindingCache())

	quarantineCh := subscribeSubject(t, nc, "billing.usage.proj-abc.quarantine.unknown_meter")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/no-such-meter")
	ce.SetSource("/test")
	mustPublishIngest(t, nc, "proj-abc", ce)

	select {
	case <-quarantineCh:
	case <-time.After(10 * time.Second):
		t.Fatal("quarantine event not received within 10 seconds")
	}
}

// --------------------------------------------------------------------------
// Integration: INVALID_DIMENSIONS quarantine path.
// --------------------------------------------------------------------------

func TestUsageConsumer_InvalidDimensions_QuarantinesCorrectly(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region"})
	fc := newFakeCache(true)
	c := buildConsumer(t, nc, fc, newTestMeterCache(md), newTestBindingCache())

	quarantineCh := subscribeSubject(t, nc, "billing.usage.proj-abc.quarantine.invalid_dimensions")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/cpu-seconds")
	ce.SetSource("/test")
	_ = ce.SetData("application/json", event.EventData{Dimensions: map[string]string{
		"region":   "us-east-1",
		"unknownX": "oops",
	}})
	mustPublishIngest(t, nc, "proj-abc", ce)

	select {
	case <-quarantineCh:
	case <-time.After(10 * time.Second):
		t.Fatal("quarantine event not received within 10 seconds")
	}
}

// --------------------------------------------------------------------------
// Integration: ATTRIBUTION_FAILURE — no binding for project.
// --------------------------------------------------------------------------

func TestUsageConsumer_NoBinding_QuarantinesAttributionFailure(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region"})
	fc := newFakeCache(true) // no bindings
	c := buildConsumer(t, nc, fc, newTestMeterCache(md), newTestBindingCache())

	quarantineCh := subscribeSubject(t, nc, "billing.usage.proj-abc.quarantine.attribution_failure")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/cpu-seconds")
	ce.SetSource("/test")
	_ = ce.SetData("application/json", event.EventData{Dimensions: map[string]string{"region": "us-east-1"}})
	mustPublishIngest(t, nc, "proj-abc", ce)

	select {
	case <-quarantineCh:
	case <-time.After(10 * time.Second):
		t.Fatal("quarantine event not received within 10 seconds")
	}
}

// --------------------------------------------------------------------------
// Integration: ATTRIBUTION_FAILURE — archived account.
// --------------------------------------------------------------------------

func TestUsageConsumer_ArchivedAccount_QuarantinesAttributionFailure(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	md := publishedMD("cpu-seconds", "compute.miloapis.com/instance/cpu-seconds", []string{"region"})
	binding := activeBinding("binding-1", "proj-archived", "acct-archived")
	archivedAccount := billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acct-archived", Namespace: "default"},
		Spec:       billingv1alpha1.BillingAccountSpec{CurrencyCode: "USD"},
		Status:     billingv1alpha1.BillingAccountStatus{Phase: billingv1alpha1.BillingAccountPhaseArchived},
	}

	fc := newFakeCache(true, archivedAccount)
	c := buildConsumer(t, nc, fc, newTestMeterCache(md), newTestBindingCache(binding))

	quarantineCh := subscribeSubject(t, nc, "billing.usage.proj-archived.quarantine.attribution_failure")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/cpu-seconds")
	ce.SetSource("/test")
	_ = ce.SetData("application/json", event.EventData{Dimensions: map[string]string{"region": "us-east-1"}})
	mustPublishIngest(t, nc, "proj-archived", ce)

	select {
	case <-quarantineCh:
	case <-time.After(10 * time.Second):
		t.Fatal("quarantine event not received within 10 seconds")
	}
}

// --------------------------------------------------------------------------
// Integration: billing_validation_rejections_total increments per quarantine.
// --------------------------------------------------------------------------

func TestUsageConsumer_RejectionCounter_IncrementsPerEvent(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	mp, ctr := newCountingMeterProvider()
	fc := newFakeCache(true)
	c := &UsageConsumer{
		Cache:         fc,
		NC:            nc,
		MeterCache:    newTestMeterCache(), // empty → all events → UNKNOWN_METER
		BindingCache:  newTestBindingCache(),
		MeterProvider: mp,
		Logger:        logr.Discard(),
	}

	quarantineCh := subscribeSubject(t, nc, "billing.usage.proj-ctr.quarantine.unknown_meter")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	const numEvents = 3
	for i := range numEvents {
		ce := cloudevents.NewEvent()
		ce.SetID(fmt.Sprintf("01HZZZZZZZZZZZZZZZZZZZZ%02d", i))
		ce.SetType("no-such-meter")
		ce.SetSource("/test")
		mustPublishIngest(t, nc, "proj-ctr", ce)
	}

	for range numEvents {
		select {
		case <-quarantineCh:
		case <-time.After(10 * time.Second):
			t.Fatal("quarantine event not received within 10 seconds")
		}
	}

	// The NATS subscribe callback fires in a separate goroutine from the
	// consumer event loop. recordRejection is called synchronously in the
	// consumer loop before the quarantine publish, but the subscriber goroutine
	// receiving the message may race with the counter update. Poll briefly
	// to ensure the counter reflects all increments.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := ctr.Load(); got == int64(numEvents) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ctr.Load(); got != int64(numEvents) {
		t.Errorf("expected rejection counter=%d, got %d", numEvents, got)
	}
}

// --------------------------------------------------------------------------
// Integration: Deprecated meter still passes validation and reaches valid.
// --------------------------------------------------------------------------

func TestUsageConsumer_DeprecatedMeter_PassesValidation(t *testing.T) {
	_, nc := startTestNATSServer(t)
	createBillingUsageStream(t, nc)

	deprecatedMD := billingv1alpha1.MeterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-seconds-deprecated"},
		Spec: billingv1alpha1.MeterDefinitionSpec{
			MeterName:   "compute.miloapis.com/instance/cpu-seconds",
			Phase:       billingv1alpha1.PhaseDeprecated,
			DisplayName: "CPU Seconds (deprecated)",
			Measurement: billingv1alpha1.MeterMeasurement{
				Aggregation: billingv1alpha1.MeterAggregationSum,
				Unit:        "s",
				Dimensions:  []string{"region"},
			},
			Billing: billingv1alpha1.MeterBilling{
				ConsumedUnit: "s",
				PricingUnit:  "h",
			},
			MonitoredResourceTypes: []string{"instance"},
		},
	}
	binding := activeBinding("binding-1", "proj-deprecated", "acct-1")
	account := readyAccount("acct-1")

	fc := newFakeCache(true, account)
	c := buildConsumer(t, nc, fc, newTestMeterCache(deprecatedMD), newTestBindingCache(binding))

	validCh := subscribeSubject(t, nc, "billing.usage.proj-deprecated.valid")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Start(ctx) }()
	t.Cleanup(cancel)

	ce := cloudevents.NewEvent()
	ce.SetID("01HZZZZZZZZZZZZZZZZZZZZZZ")
	ce.SetType("compute.miloapis.com/instance/cpu-seconds")
	ce.SetSource("/test")
	_ = ce.SetData("application/json", event.EventData{Dimensions: map[string]string{"region": "us-east-1"}})
	mustPublishIngest(t, nc, "proj-deprecated", ce)

	select {
	case msg := <-validCh:
		var enriched cloudevents.Event
		if err := json.Unmarshal(msg.Data, &enriched); err != nil {
			t.Fatalf("unmarshaling valid event: %v", err)
		}
		ext := enriched.Extensions()
		billingRef, _ := ext["billingaccountref"].(string)
		if billingRef != "acct-1" {
			t.Errorf("expected billingaccountref=acct-1, got %q", billingRef)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("valid event not received: deprecated meter should pass validation")
	}
}
