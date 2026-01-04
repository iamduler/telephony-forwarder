package consumer

import (
	"context"
	"encoding/json"
	"time"

	"calleventhub/internal/config"
	"calleventhub/internal/forwarder"
	"calleventhub/internal/logger"
	"calleventhub/internal/nats"

	natsgo "github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// ConsumerService consumes events from NATS and forwards them
type ConsumerService struct {
	consumer *nats.Consumer
	forwarder *forwarder.Forwarder
	config   *config.Config
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewConsumerService creates a new consumer service
func NewConsumerService(cfg *config.Config, natsConsumer *nats.Consumer, fwd *forwarder.Forwarder) *ConsumerService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConsumerService{
		consumer:  natsConsumer,
		forwarder: fwd,
		config:    cfg,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start starts consuming messages and forwarding them
func (cs *ConsumerService) Start() error {
	logger.Logger.Info("Starting event consumer")

	msgChan := cs.consumer.Messages()

	for {
		select {
		case <-cs.ctx.Done():
			logger.Logger.Info("Consumer context cancelled, stopping")
			return nil
		case msg, ok := <-msgChan:
			if !ok {
				logger.Logger.Info("Message channel closed")
				return nil
			}

			// Process message in a goroutine to allow concurrent processing
			go cs.processMessage(msg)
		}
	}
}

// processMessage processes a single message
func (cs *ConsumerService) processMessage(msg *natsgo.Msg) {
	// Extract metadata for logging
	metadata, err := msg.Metadata()
	deliveryAttempt := 1
	if err == nil && metadata != nil {
		deliveryAttempt = int(metadata.NumDelivered)
	}

	// Parse event to extract domain
	var event struct {
		EventID string `json:"event_id"`
		Domain  string `json:"domain"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		logger.Logger.Error("Failed to parse event",
			zap.Error(err),
			zap.Int("delivery_attempt", deliveryAttempt),
		)
		// NAK the message to trigger redelivery
		if err := cs.consumer.Nak(msg); err != nil {
			logger.Logger.Error("Failed to NAK message", zap.Error(err))
		}
		return
	}

	// Create context with timeout for forwarding
	ctx, cancel := context.WithTimeout(cs.ctx, 3*time.Second)
	defer cancel()

	// Forward event to all endpoints
	err = cs.forwarder.ForwardEvent(ctx, msg.Data, event.Domain, deliveryAttempt)
	if err != nil {
		logger.Logger.Error("Failed to forward event",
			zap.String("event_id", event.EventID),
			zap.String("domain", event.Domain),
			zap.String("type", event.Type),
			zap.Int("delivery_attempt", deliveryAttempt),
			zap.Error(err),
		)
		// DO NOT acknowledge - let JetStream redeliver after ack_wait expires
		// The message will be redelivered automatically by JetStream
		return
	}

	// All endpoints succeeded - acknowledge the message
	if err := cs.consumer.Ack(msg); err != nil {
		logger.Logger.Error("Failed to acknowledge message",
			zap.String("event_id", event.EventID),
			zap.Error(err),
		)
		return
	}

	logger.Logger.Info("Event processed and acknowledged",
		zap.String("event_id", event.EventID),
		zap.String("domain", event.Domain),
		zap.String("type", event.Type),
		zap.Int("delivery_attempt", deliveryAttempt),
	)
}

// Stop stops the consumer service
func (cs *ConsumerService) Stop() {
	logger.Logger.Info("Stopping consumer service")
	cs.cancel()
}

