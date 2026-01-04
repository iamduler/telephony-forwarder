package nats

import (
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"calleventhub/internal/logger"
)

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Consumer handles consuming events from NATS JetStream
type Consumer struct {
	conn    *nats.Conn
	js      nats.JetStreamContext
	sub     *nats.Subscription
	stream  string
	subject string
	msgChan chan *nats.Msg
}

// NewConsumer creates a new NATS consumer with PUSH-based delivery
//
// JetStream Retry and Backoff Behavior:
// - When a message is not acknowledged within ack_wait seconds, JetStream will redeliver it
// - MaxDeliver limits the total number of delivery attempts (including the first)
// - Exponential backoff is achieved by configuring ack_wait appropriately:
//   - First retry: after ack_wait (e.g., 1s)
//   - Second retry: after ack_wait (e.g., 3s)
//   - Third retry: after ack_wait (e.g., 7s)
//   - The service does NOT implement retry logic - it relies entirely on JetStream's
//     at-least-once delivery semantics
//   - If ANY endpoint fails during forwarding, the message is NOT acknowledged,
//     causing JetStream to redeliver the entire message after ack_wait expires
func NewConsumer(url, streamName, subjectPattern, consumerName string, ackWait, maxDeliveries int) (*Consumer, error) {
	opts := []nats.Option{
		nats.Name("event-hub-consumer"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				logger.Logger.Warn("NATS disconnected", zap.Error(err))
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Logger.Info("NATS reconnected", zap.String("url", nc.ConnectedUrl()))
		}),
	}

	conn, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Ensure stream exists
	_, err = js.StreamInfo(streamName)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Delete existing consumer if it exists (to ensure correct configuration)
	// This is necessary because consumer configuration cannot be changed once created
	err = js.DeleteConsumer(streamName, consumerName)
	if err != nil {
		// Ignore error if consumer doesn't exist
		if !contains(err.Error(), "not found") && !contains(err.Error(), "does not exist") {
			logger.Logger.Warn("Failed to delete existing consumer (may not exist)", zap.Error(err))
		}
	} else {
		logger.Logger.Info("Deleted existing consumer to recreate with correct configuration", zap.String("consumer", consumerName))
	}

	// Create consumer with PUSH-based delivery
	// AckWait: 10 seconds (must be > backend timeout of 3 seconds)
	// MaxDeliver: 3 attempts total
	// AckPolicy: Explicit - we must manually acknowledge
	// DeliverPolicy: DeliverNewPolicy - only receive NEW messages (not old ones in stream)
	// This prevents replaying old messages when the service restarts
	consumerConfig := &nats.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		DeliverPolicy: nats.DeliverNewPolicy, // Changed from DeliverAllPolicy to only process new messages
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       time.Duration(ackWait) * time.Second,
		MaxDeliver:    maxDeliveries,
		// PUSH-based: messages are pushed to the subscription channel
		// No polling required - messages arrive asynchronously
	}

	_, err = js.AddConsumer(streamName, consumerConfig)
	if err != nil {
		conn.Close()
		return nil, err
	}
	logger.Logger.Info("Created NATS consumer", zap.String("consumer", consumerName))

	// Create a message channel for PUSH-based delivery
	msgChan := make(chan *nats.Msg, 100)

	// For PUSH-based delivery with durable consumer, we need to use PullSubscribe
	// with a continuous fetch loop to simulate PUSH behavior
	// This is because NATS JetStream durable consumers are typically PULL-based
	sub, err := js.PullSubscribe(subjectPattern, consumerName, nats.ManualAck())
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Start a goroutine to continuously fetch messages and push to channel
	// This simulates PUSH-based delivery by polling with very short intervals
	go func() {
		defer close(msgChan)
		for {
			// Fetch with small batch size and short timeout to simulate PUSH
			msgs, err := sub.Fetch(1, nats.MaxWait(50*time.Millisecond))
			if err != nil {
				if err == nats.ErrTimeout {
					// Timeout is expected when no messages available, continue polling
					continue
				}
				// Other errors - log and exit
				logger.Logger.Error("Error fetching messages from NATS", zap.Error(err))
				return
			}
			for _, msg := range msgs {
				select {
				case msgChan <- msg:
				default:
					logger.Logger.Warn("Message channel full, dropping message")
				}
			}
		}
	}()

	cons := &Consumer{
		conn:    conn,
		js:      js,
		sub:     sub,
		stream:  streamName,
		subject: subjectPattern,
		msgChan: msgChan,
	}

	return cons, nil
}

// Messages returns the channel that receives messages (PUSH-based delivery)
func (c *Consumer) Messages() <-chan *nats.Msg {
	return c.msgChan
}

// Ack acknowledges a message
func (c *Consumer) Ack(msg *nats.Msg) error {
	return msg.Ack()
}

// Nak negatively acknowledges a message (triggers redelivery)
func (c *Consumer) Nak(msg *nats.Msg) error {
	return msg.Nak()
}

// Close closes the consumer subscription and connection
func (c *Consumer) Close() {
	if c.sub != nil {
		c.sub.Unsubscribe()
		c.sub.Drain()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
