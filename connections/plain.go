package connections

import (
	"context"
	"errors"
	"github.com/nats-io/nats.go"
	"gocloud.dev/pubsub/driver"
	"net/url"
	"time"
)

func NewPlain(natsConn *nats.Conn) ConnectionMux {
	return &natsConnection{natsConnection: natsConn}
}

type natsConnection struct {
	// Connection to use for communication with the server.
	natsConnection *nats.Conn
}

func (c *natsConnection) Raw() interface{} {
	return c.natsConnection
}

func (c *natsConnection) CreateSubscription(ctx context.Context, opts *SubscriptionOptions) (Queue, error) {

	if opts != nil && opts.ConsumerQueue != "" {

		subsc, err := c.natsConnection.QueueSubscribeSync(opts.ConsumerSubject, opts.ConsumerQueue)
		if err != nil {
			return nil, err
		}

		return &natsConsumer{consumer: subsc, durable: true, batchFetchTimeout: 1 * time.Second}, nil
	}
	subsc, err := c.natsConnection.SubscribeSync(opts.ConsumerSubject)
	if err != nil {
		return nil, err
	}

	return &natsConsumer{consumer: subsc, durable: false}, nil

}

func (c *natsConnection) PublishMessage(_ context.Context, msg *nats.Msg) (string, error) {
	var err error
	if err = c.natsConnection.PublishMsg(msg); err != nil {
		return "", err
	}

	return "", nil
}

type natsConsumer struct {
	consumer          *nats.Subscription
	durable           bool
	batchFetchTimeout time.Duration
}

func (q *natsConsumer) IsDurable() bool {
	return q.durable
}

func (q *natsConsumer) Unsubscribe() error {
	return q.consumer.Unsubscribe()
}

func (q *natsConsumer) ReceiveMessages(ctx context.Context, _ int) ([]*driver.Message, error) {

	var messages []*driver.Message

	msg, err := q.consumer.NextMsg(q.batchFetchTimeout)
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) {
			return messages, nil
		}
		return nil, err
	}
	driverMsg, err := decodeMessage(msg)

	if err != nil {
		return messages, err
	}

	messages = append(messages, driverMsg)

	return messages, nil

}

func (q *natsConsumer) Ack(ctx context.Context, ids []driver.AckID) error {
	for _, id := range ids {
		msg, ok := id.(*nats.Msg)
		if !ok {
			continue
		}
		_ = msg.Ack()
	}

	return nil
}

func (q *natsConsumer) Nack(ctx context.Context, ids []driver.AckID) error {
	for _, id := range ids {
		msg, ok := id.(*nats.Msg)
		if !ok {
			continue
		}
		_ = msg.Nak()
	}

	return nil
}

func messageAsFunc(msg *nats.Msg) func(interface{}) bool {
	return func(i any) bool {
		p, ok := i.(**nats.Msg)
		if !ok {
			return false
		}
		*p = msg
		return true
	}
}

func decodeMessage(msg *nats.Msg) (*driver.Message, error) {
	if msg == nil {
		return nil, nats.ErrInvalidMsg
	}

	dm := driver.Message{
		AsFunc: messageAsFunc(msg),
		Body:   msg.Data,
	}

	if msg.Header != nil {
		dm.Metadata = map[string]string{}
		for k, v := range msg.Header {
			var sv string
			if len(v) > 0 {
				sv = v[0]
			}
			kb, err := url.QueryUnescape(k)
			if err != nil {
				return nil, err
			}
			vb, err := url.QueryUnescape(sv)
			if err != nil {
				return nil, err
			}
			dm.Metadata[kb] = vb
		}
	}

	dm.AckID = msg

	return &dm, nil
}
