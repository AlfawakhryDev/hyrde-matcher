package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes events to Kafka.
//
// Two ways to send: PublishAsync never blocks (for the hot path, where a full
// buffer means drop and count rather than slow down a match), and
// PublishSync waits for the broker's acknowledgement (for events that must
// not be lost, like a milestone being delivered).
type Producer struct {
	client  *kgo.Client
	dropped atomic.Int64
	failed  atomic.Int64
}

func NewProducer(brokers []string) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ProducerLinger(5*time.Millisecond), // batch for 5ms: fewer, bigger requests
		kgo.MaxBufferedRecords(10_000),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, err
	}
	return &Producer{client: client}, nil
}

func record(topic string, e Event) (*kgo.Record, error) {
	value, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	// Keying by engineer keeps one engineer's events in order on one partition.
	key := e.EngineerID
	if key == "" {
		key = e.MilestoneID
	}
	return &kgo.Record{Topic: topic, Key: []byte(key), Value: value}, nil
}

// PublishAsync queues an event and returns at once. Drops and failures are
// counted (Dropped, Failed) rather than returned: the caller has moved on.
func (p *Producer) PublishAsync(topic string, e Event) {
	r, err := record(topic, e)
	if err != nil {
		p.dropped.Add(1)
		return
	}
	// TryProduce fails immediately instead of blocking when the buffer is full.
	p.client.TryProduce(context.Background(), r, func(_ *kgo.Record, err error) {
		if errors.Is(err, kgo.ErrMaxBuffered) {
			p.dropped.Add(1)
		} else if err != nil {
			p.failed.Add(1)
		}
	})
}

// PublishSync sends an event and waits until Kafka has it.
func (p *Producer) PublishSync(ctx context.Context, topic string, e Event) error {
	r, err := record(topic, e)
	if err != nil {
		return err
	}
	return p.client.ProduceSync(ctx, r).FirstErr()
}

func (p *Producer) Dropped() int64 { return p.dropped.Load() }
func (p *Producer) Failed() int64  { return p.failed.Load() }

// Ping checks the brokers are reachable, for readiness probes.
func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }

// Close flushes what is buffered, then disconnects.
func (p *Producer) Close(ctx context.Context) {
	_ = p.client.Flush(ctx)
	p.client.Close()
}
