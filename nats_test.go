// Copyright 2019 The Go Cloud Development Kit Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package natspubsub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pitabwire/natspubsub/connections"
	"gocloud.dev/pubsub/batcher"
	"testing"

	"gocloud.dev/gcerrors"
	"gocloud.dev/pubsub"
	"gocloud.dev/pubsub/driver"
	"gocloud.dev/pubsub/drivertest"

	"github.com/nats-io/nats-server/v2/server"
	gnatsd "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

const (
	testServerUrlFmt = "nats://127.0.0.1:%d"
	testPort         = 11222
	benchPort        = 9222
)

func newPlainHarness(ctx context.Context, t *testing.T) (drivertest.Harness, error) {
	opts := gnatsd.DefaultTestOptions
	opts.Port = testPort
	s := gnatsd.RunServer(&opts)
	nc, err := nats.Connect(fmt.Sprintf(testServerUrlFmt, testPort))
	if err != nil {
		return nil, err
	}

	plainConn := connections.NewPlain(nc)

	return &harness{s: s, conn: plainConn}, nil
}

func newJetstreamHarness(ctx context.Context, t *testing.T) (drivertest.Harness, error) {
	opts := gnatsd.DefaultTestOptions
	opts.Port = testPort
	opts.JetStream = true
	s := gnatsd.RunServer(&opts)
	nc, err := nats.Connect(fmt.Sprintf(testServerUrlFmt, testPort))
	if err != nil {
		return nil, err
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}

	jsConn := connections.NewJetstream(js)

	return &harness{s: s, conn: jsConn}, nil
}

type harness struct {
	s    *server.Server
	conn connections.Connection
}

func (h *harness) CreateTopic(ctx context.Context, testName string) (driver.Topic, func(), error) {
	cleanup := func() {}

	pOpts := &connections.TopicOptions{Subject: testName}

	dt, err := openTopic(ctx, h.conn, pOpts)
	if err != nil {
		return nil, nil, err
	}
	return dt, cleanup, nil
}

func (h *harness) MakeNonexistentTopic(ctx context.Context) (driver.Topic, error) {
	// A nil *topic behaves like a nonexistent topic.
	return (*topic)(nil), nil
}

func defaultSubOptions(subject, testName string) *connections.SubscriptionOptions {

	sOpts := &connections.SetupOptions{
		StreamName:   fmt.Sprintf("test_stream_%s", testName),
		Subjects:     []string{subject},
		DurableQueue: testName,
	}

	opts := &connections.SubscriptionOptions{
		ConsumerMaxBatchTimeoutMs: 1000,
		SetupOpts:                 sOpts,
	}
	return opts
}

func (h *harness) CreateSubscription(ctx context.Context, dt driver.Topic, testName string) (driver.Subscription, func(), error) {

	var tp connections.Topic
	dt.As(&tp)

	opts := defaultSubOptions(tp.Subject(), testName)
	ds, err := openSubscription(ctx, h.conn, opts)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		var queue connections.Queue
		if ds.As(&queue) {
			queue.Unsubscribe()
		}
	}
	return ds, cleanup, nil
}

func (h *harness) CreateQueueSubscription(ctx context.Context, dt driver.Topic, testName string) (driver.Subscription, func(), error) {

	var tp connections.Topic
	dt.As(&tp)

	opts := defaultSubOptions(tp.Subject(), testName)

	ds, err := openSubscription(ctx, h.conn, opts)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		var sub connections.Queue
		if ds.As(&sub) {
			sub.Unsubscribe()
		}
	}
	return ds, cleanup, nil
}

func (h *harness) MakeNonexistentSubscription(ctx context.Context) (driver.Subscription, func(), error) {
	return (*subscription)(nil), func() {}, nil
}

func (h *harness) Close() {
	h.s.Shutdown()
}

func (h *harness) MaxBatchSizes() (int, int) { return 0, 0 }

func (*harness) SupportsMultipleSubscriptions() bool { return true }

type plainNatsAsTest struct {
}

func (plainNatsAsTest) Name() string {
	return "nats test"
}

func (plainNatsAsTest) TopicCheck(topic *pubsub.Topic) error {
	var c2 connections.Topic
	if topic.As(c2) {
		return fmt.Errorf("cast succeeded for %T, want failure", &c2)
	}
	var c3 connections.Topic
	if !topic.As(&c3) {
		return fmt.Errorf("cast failed for %T", &c3)
	}
	return nil
}

func (plainNatsAsTest) SubscriptionCheck(sub *pubsub.Subscription) error {
	var c2 connections.Queue
	if sub.As(c2) {
		return fmt.Errorf("cast succeeded for %T, want failure", &c2)
	}
	var c3 connections.Queue
	if !sub.As(&c3) {
		return fmt.Errorf("cast failed for %T", &c3)
	}
	return nil
}

func (plainNatsAsTest) TopicErrorCheck(t *pubsub.Topic, err error) error {
	var dummy string
	if t.ErrorAs(err, &dummy) {
		return fmt.Errorf("cast succeeded for %T, want failure", &dummy)
	}
	return nil
}

func (plainNatsAsTest) SubscriptionErrorCheck(s *pubsub.Subscription, err error) error {
	var dummy string
	if s.ErrorAs(err, &dummy) {
		return fmt.Errorf("cast succeeded for %T, want failure", &dummy)
	}
	return nil
}

func (plainNatsAsTest) MessageCheck(m *pubsub.Message) error {
	var pm *nats.Msg
	if m.As(pm) {
		return fmt.Errorf("cast succeeded for %T, want failure", &pm)
	}
	var ppm *nats.Msg
	if !m.As(&ppm) {
		return fmt.Errorf("cast failed for %T", &ppm)
	}
	return nil
}

func (n plainNatsAsTest) BeforeSend(as func(interface{}) bool) error {
	var pm *nats.Msg
	if as(pm) {
		return fmt.Errorf("cast succeeded for %T, want failure", &pm)
	}

	var ppm *nats.Msg
	if !as(&ppm) {
		return fmt.Errorf("cast failed for %T", &ppm)
	}
	return nil
}

func (plainNatsAsTest) AfterSend(as func(interface{}) bool) error {
	return nil
}

type jetstreamAsTest struct {
	plainNatsAsTest
}

func (jetstreamAsTest) TopicCheck(topic *pubsub.Topic) error {
	var c2 connections.Topic
	if topic.As(c2) {
		return fmt.Errorf("cast succeeded for %T, want failure", &c2)
	}
	var c3 connections.Topic
	if !topic.As(&c3) {
		return fmt.Errorf("cast failed for %T", &c3)
	}
	return nil
}

func (jetstreamAsTest) SubscriptionCheck(sub *pubsub.Subscription) error {
	var c2 connections.Queue
	if sub.As(c2) {
		return fmt.Errorf("cast succeeded for %T, want failure", &c2)
	}
	var c3 connections.Queue
	if !sub.As(&c3) {
		return fmt.Errorf("cast failed for %T", &c3)
	}
	return nil
}

func (jetstreamAsTest) MessageCheck(m *pubsub.Message) error {
	var pm jetstream.Msg
	if m.As(pm) {
		return fmt.Errorf("cast succeeded for %T, want failure", &pm)
	}
	var ppm jetstream.Msg
	if !m.As(&ppm) {
		return fmt.Errorf("cast failed for %T", ppm)
	}
	return nil
}

func (n jetstreamAsTest) BeforeSend(as func(interface{}) bool) error {
	var pm nats.Msg
	if as(pm) {
		return fmt.Errorf("cast succeeded for %T, want failure", &pm)
	}

	var ppm *nats.Msg
	if !as(&ppm) {
		return fmt.Errorf("cast failed for %T", &ppm)
	}
	return nil
}

func TestConformanceJetstream(t *testing.T) {
	asTests := []drivertest.AsTest{jetstreamAsTest{}}
	drivertest.RunConformanceTests(t, newJetstreamHarness, asTests)
}

func TestConformancePlain(t *testing.T) {
	asTests := []drivertest.AsTest{plainNatsAsTest{}}
	drivertest.RunConformanceTests(t, newPlainHarness, asTests)
}

// These are natspubsub specific to increase coverage.
// If we only send a body we should be able to get that from a direct NATS subscriber.
func TestPlainInteropWithDirectNATS(t *testing.T) {
	ctx := context.Background()
	dh, err := newPlainHarness(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	defer dh.Close()
	conn := dh.(*harness).conn

	const topic = "foo"
	md := map[string]string{"a": "1", "b": "2", "c": "3"}
	body := []byte("hello")

	// Send a message using Go CDK and receive it using NATS directly.
	pt, err := OpenTopic(ctx, conn, &connections.TopicOptions{Subject: topic})
	if err != nil {
		t.Fatal(err)
	}
	defer pt.Shutdown(ctx)

	natsConn := conn.Raw().(*nats.Conn)

	nsub, _ := natsConn.SubscribeSync(topic)
	if err = pt.Send(ctx, &pubsub.Message{Body: body, Metadata: md}); err != nil {
		t.Fatal(err)
	}
	m, err := nsub.NextMsgWithContext(ctx)
	if err != nil {
		t.Fatalf(err.Error())
	}

	if !bytes.Equal(m.Data, body) {
		t.Fatalf("Data did not match. %q vs %q\n", m.Data, body)
	}
	for k, v := range md {
		if m.Header.Get(k) != v {
			t.Fatalf("Metadata %q did not match. %q vs %q\n", k, m.Header.Get(k), v)
		}
	}

	// Send a message using NATS directly and receive it using Go CDK.
	opts := defaultSubOptions(topic, t.Name())

	ps, err := OpenSubscription(ctx, conn, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Shutdown(ctx)
	if err = natsConn.Publish(topic, body); err != nil {
		t.Fatal(err)
	}
	msg, err := ps.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Ack()
	if !bytes.Equal(msg.Body, body) {
		t.Fatalf("Data did not match. %q vs %q\n", m.Data, body)
	}
}

func TestJetstreamInteropWithDirectNATS(t *testing.T) {
	ctx := context.Background()
	dh, err := newJetstreamHarness(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	defer dh.Close()
	conn := dh.(*harness).conn

	const topic = "foo"
	const topic2 = "flow"
	md := map[string]string{"a": "1", "b": "2", "c": "3"}
	body := []byte("hello")

	// Send a message using Go CDK and receive it using NATS directly.
	pt, err := OpenTopic(ctx, conn, &connections.TopicOptions{Subject: topic})
	if err != nil {
		t.Fatal(err)
	}
	defer pt.Shutdown(ctx)

	js := conn.Raw().(jetstream.JetStream)

	stream, err := js.Stream(ctx, topic)
	if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
		t.Fatal(err)
	}

	if stream == nil {

		streamConfig := jetstream.StreamConfig{
			Name:     topic,
			Subjects: []string{topic},
		}

		stream, err = js.CreateStream(ctx, streamConfig)
		if err != nil {
			t.Fatal(err)
		}

	}

	// Create durable consumer
	c, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   topic,
		AckPolicy: jetstream.AckExplicitPolicy,
	})

	if err != nil {
		t.Fatal(err)
	}

	if err = pt.Send(ctx, &pubsub.Message{Body: body, Metadata: md}); err != nil {
		t.Fatal(err)
	}
	m, err := c.Next()
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !bytes.Equal(m.Data(), body) {
		t.Fatalf("Data did not match. %q vs %q\n", m.Data(), body)
	}
	for k, v := range md {
		if m.Headers().Get(k) != v {
			t.Fatalf("Metadata %q did not match. %q vs %q\n", k, m.Headers().Get(k), v)
		}
	}

	// Send a message using NATS directly and receive it using Go CDK.
	opts := defaultSubOptions(topic2, fmt.Sprintf("2_%s", t.Name()))

	ps, err := OpenSubscription(ctx, conn, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Shutdown(ctx)

	if _, err = js.Publish(ctx, topic2, body); err != nil {
		t.Fatal(err)
	}
	msg, err := ps.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Ack()
	if !bytes.Equal(msg.Body, body) {
		t.Fatalf("Data did not match. %q vs %q\n", m.Data(), body)
	}
}

func TestErrorCode(t *testing.T) {
	ctx := context.Background()
	dh, err := newJetstreamHarness(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	defer dh.Close()
	h := dh.(*harness)

	// Topics
	dt, err := openTopic(ctx, h.conn, &connections.TopicOptions{Subject: "bar"})
	if err != nil {
		t.Fatal(err)
	}

	if gce := dt.ErrorCode(nil); gce != gcerrors.OK {
		t.Fatalf("Expected %v, got %v", gcerrors.OK, gce)
	}
	if gce := dt.ErrorCode(context.Canceled); gce != gcerrors.Canceled {
		t.Fatalf("Expected %v, got %v", gcerrors.Canceled, gce)
	}
	if gce := dt.ErrorCode(nats.ErrBadSubject); gce != gcerrors.FailedPrecondition {
		t.Fatalf("Expected %v, got %v", gcerrors.FailedPrecondition, gce)
	}
	if gce := dt.ErrorCode(nats.ErrAuthorization); gce != gcerrors.PermissionDenied {
		t.Fatalf("Expected %v, got %v", gcerrors.PermissionDenied, gce)
	}
	if gce := dt.ErrorCode(nats.ErrMaxPayload); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}
	if gce := dt.ErrorCode(nats.ErrReconnectBufExceeded); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}

	// Subscriptions
	opts := defaultSubOptions("bar", t.Name())

	ds, err := openSubscription(ctx, h.conn, opts)
	if err != nil {
		t.Fatal(err)
	}
	if gce := ds.ErrorCode(nil); gce != gcerrors.OK {
		t.Fatalf("Expected %v, got %v", gcerrors.OK, gce)
	}
	if gce := ds.ErrorCode(context.Canceled); gce != gcerrors.Canceled {
		t.Fatalf("Expected %v, got %v", gcerrors.Canceled, gce)
	}
	if gce := ds.ErrorCode(nats.ErrBadSubject); gce != gcerrors.FailedPrecondition {
		t.Fatalf("Expected %v, got %v", gcerrors.FailedPrecondition, gce)
	}
	if gce := ds.ErrorCode(nats.ErrBadSubscription); gce != gcerrors.NotFound {
		t.Fatalf("Expected %v, got %v", gcerrors.NotFound, gce)
	}
	if gce := ds.ErrorCode(nats.ErrTypeSubscription); gce != gcerrors.FailedPrecondition {
		t.Fatalf("Expected %v, got %v", gcerrors.FailedPrecondition, gce)
	}
	if gce := ds.ErrorCode(nats.ErrAuthorization); gce != gcerrors.PermissionDenied {
		t.Fatalf("Expected %v, got %v", gcerrors.PermissionDenied, gce)
	}
	if gce := ds.ErrorCode(nats.ErrMaxMessages); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}
	if gce := ds.ErrorCode(nats.ErrSlowConsumer); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}
	if gce := ds.ErrorCode(nats.ErrTimeout); gce != gcerrors.DeadlineExceeded {
		t.Fatalf("Expected %v, got %v", gcerrors.DeadlineExceeded, gce)
	}

	// Queue Subscription
	opts = defaultSubOptions("bar", t.Name())

	qs, err := openSubscription(ctx, h.conn, opts)
	if err != nil {
		t.Fatal(err)
	}
	if gce := qs.ErrorCode(nil); gce != gcerrors.OK {
		t.Fatalf("Expected %v, got %v", gcerrors.OK, gce)
	}
	if gce := qs.ErrorCode(context.Canceled); gce != gcerrors.Canceled {
		t.Fatalf("Expected %v, got %v", gcerrors.Canceled, gce)
	}
	if gce := qs.ErrorCode(nats.ErrBadSubject); gce != gcerrors.FailedPrecondition {
		t.Fatalf("Expected %v, got %v", gcerrors.FailedPrecondition, gce)
	}
	if gce := qs.ErrorCode(nats.ErrBadSubscription); gce != gcerrors.NotFound {
		t.Fatalf("Expected %v, got %v", gcerrors.NotFound, gce)
	}
	if gce := qs.ErrorCode(nats.ErrTypeSubscription); gce != gcerrors.FailedPrecondition {
		t.Fatalf("Expected %v, got %v", gcerrors.FailedPrecondition, gce)
	}
	if gce := qs.ErrorCode(nats.ErrAuthorization); gce != gcerrors.PermissionDenied {
		t.Fatalf("Expected %v, got %v", gcerrors.PermissionDenied, gce)
	}
	if gce := qs.ErrorCode(nats.ErrMaxMessages); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}
	if gce := qs.ErrorCode(nats.ErrSlowConsumer); gce != gcerrors.ResourceExhausted {
		t.Fatalf("Expected %v, got %v", gcerrors.ResourceExhausted, gce)
	}
	if gce := qs.ErrorCode(nats.ErrTimeout); gce != gcerrors.DeadlineExceeded {
		t.Fatalf("Expected %v, got %v", gcerrors.DeadlineExceeded, gce)
	}
}

func BenchmarkNatsQueuePubSub(b *testing.B) {
	ctx := context.Background()

	opts := gnatsd.DefaultTestOptions
	opts.Port = benchPort
	s := gnatsd.RunServer(&opts)
	defer s.Shutdown()

	nc, err := nats.Connect(fmt.Sprintf(testServerUrlFmt, benchPort))
	if err != nil {
		b.Fatal(err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		b.Fatal(err)
	}

	conn := connections.NewJetstream(js)

	h := &harness{s: s, conn: conn}

	b.Run("Jetstream", func(b *testing.B) {
		dt, cleanup, err := h.CreateTopic(ctx, b.Name())
		if err != nil {
			b.Fatal(err)
		}
		defer cleanup()

		qs, cleanup, err := h.CreateQueueSubscription(ctx, dt, b.Name())
		if err != nil {
			b.Fatal(err)
		}
		defer cleanup()

		topic := pubsub.NewTopic(dt, nil)
		defer topic.Shutdown(ctx)

		queueSub := pubsub.NewSubscription(qs, &batcher.Options{
			MaxBatchSize: 100,
			MaxHandlers:  10, // max concurrency for receives
		}, nil)
		defer queueSub.Shutdown(ctx)

		drivertest.RunBenchmarks(b, topic, queueSub)
	})

}

func BenchmarkNatsPubSub(b *testing.B) {
	ctx := context.Background()

	opts := gnatsd.DefaultTestOptions
	opts.Port = benchPort
	s := gnatsd.RunServer(&opts)
	defer s.Shutdown()

	nc, err := nats.Connect(fmt.Sprintf(testServerUrlFmt, benchPort))
	if err != nil {
		b.Fatal(err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		b.Fatal(err)
	}

	conn := connections.NewJetstream(js)

	h := &harness{s: s, conn: conn}
	b.Run("Jetstream", func(b *testing.B) {
		dt, cleanup, err := h.CreateTopic(ctx, b.Name())
		if err != nil {
			b.Fatal(err)
		}
		defer cleanup()
		ds, cleanup, err := h.CreateSubscription(ctx, dt, b.Name())
		if err != nil {
			b.Fatal(err)
		}
		defer cleanup()

		topic := pubsub.NewTopic(dt, nil)
		defer topic.Shutdown(ctx)
		sub := pubsub.NewSubscription(ds, &batcher.Options{
			MaxBatchSize: 100,
			MaxHandlers:  10, // max concurrency for receives
		}, nil)
		defer sub.Shutdown(ctx)

		drivertest.RunBenchmarks(b, topic, sub)
	})
}

func TestOpenTopicFromURL(t *testing.T) {
	ctx := context.Background()
	dh, err := newJetstreamHarness(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	defer dh.Close()

	tests := []struct {
		URL     string
		WantErr bool
	}{
		// OK.
		{"nats://localhost:11222/mytopic", false},
		// Invalid parameter.
		{"nats://localhost:11222/mytopic?param=value", true},
	}

	for _, test := range tests {
		topic, err := pubsub.OpenTopic(ctx, test.URL)
		if (err != nil) != test.WantErr {
			t.Errorf("%s: got error %v, want error %v", test.URL, err, test.WantErr)
		}
		if topic != nil {
			topic.Shutdown(ctx)
		}
	}
}

func TestOpenSubscriptionFromURL(t *testing.T) {
	ctx := context.Background()
	dh, err := newJetstreamHarness(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	defer dh.Close()

	tests := []struct {
		URL     string
		WantErr bool
	}{
		// OK.
		{"nats://localhost:11222/mytopic", false},
		// Invalid parameter.
		{"nats://localhost:11222/mytopic?param=value", true},
		// Queue URL Parameter for QueueSubscription.
		{"nats://localhost:11222/mytopic?queue=queue1", false},
		// Multiple values for Queue URL Parameter for QueueSubscription.
		{"nats://localhost:11222/mytopic?subject=queue1&subject=queue2", true},
	}

	for _, test := range tests {
		sub, err := pubsub.OpenSubscription(ctx, test.URL)
		if (err != nil) != test.WantErr {
			t.Errorf("%s: got error %v, want error %v", test.URL, err, test.WantErr)
		}
		if sub != nil {
			sub.Shutdown(ctx)
		}
	}
}
