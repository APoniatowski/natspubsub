package connections

import (
	"context"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"gocloud.dev/pubsub/driver"
	"net/url"
	"time"
)

func NewPlain(natsConn *nats.Conn) Connection {
	return &plainConnection{natsConnection: natsConn}
}

type plainConnection struct {
	// Connection to use for communication with the server.
	natsConnection *nats.Conn
}

func (c *plainConnection) Raw() interface{} {
	return c.natsConnection
}

func (c *plainConnection) CreateTopic(ctx context.Context, opts *TopicOptions) (Topic, error) {

	return &plainNatsTopic{subject: opts.Subject, plainConn: c.natsConnection}, nil
}

func (c *plainConnection) CreateSubscription(ctx context.Context, opts *SubscriptionOptions) (Queue, error) {

	fmt.Println("---------------------------------------------------------------------------")
	fmt.Printf(" subscribing to : [%s] {%s}", opts.SetupOpts.Subjects[0], opts.SetupOpts.DurableQueue)
	fmt.Println("--------------------------------------------------------------------------")

	sOpts := opts.SetupOpts

	//We force the batch fetch size to 1, as only jetstream enabled connections can do batch fetches
	// see: https://pkg.go.dev/github.com/nats-io/nats.go@v1.30.1#Conn.QueueSubscribeSync
	opts.ConsumerMaxBatchSize = 1

	if sOpts.DurableQueue != "" {

		subsc, err := c.natsConnection.QueueSubscribeSync(sOpts.Subjects[0], sOpts.DurableQueue)
		if err != nil {
			return nil, err
		}

		return &natsConsumer{consumer: subsc, durable: true,
			batchFetchTimeout: time.Duration(opts.ConsumerMaxBatchTimeoutMs) * time.Millisecond}, nil
	}

	// Using nats without any form of queue mechanism is fine only where
	// loosing some messages is ok as this essentially is an atmost once delivery situation here.
	subsc, err := c.natsConnection.SubscribeSync(sOpts.Subjects[0])
	if err != nil {
		return nil, err
	}

	return &natsConsumer{consumer: subsc, durable: false,
		batchFetchTimeout: time.Duration(opts.ConsumerMaxBatchTimeoutMs) * time.Millisecond}, nil

}

type plainNatsTopic struct {
	subject   string
	plainConn *nats.Conn
}

func (t *plainNatsTopic) Subject() string {
	return t.subject
}
func (t *plainNatsTopic) PublishMessage(_ context.Context, msg *nats.Msg) (string, error) {
	var err error
	if err = t.plainConn.PublishMsg(msg); err != nil {
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

func (q *natsConsumer) ReceiveMessages(ctx context.Context, batchSize int) ([]*driver.Message, error) {

	var messages []*driver.Message

	for i := 0; i < batchSize; i++ {

		msg, err := q.consumer.NextMsg(q.batchFetchTimeout)
		if err != nil {
			fmt.Printf(" error occurred : [%s] ", err)
			if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
				return messages, nil
			}
			return nil, err
		}
		driverMsg, err := decodeMessage(msg)

		if err != nil {
			fmt.Printf(" error decoding : [%s] ", err)
			return nil, err
		}

		messages = append(messages, driverMsg)
	}

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
