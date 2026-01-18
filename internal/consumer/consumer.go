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
	"go.uber.org/zap/zapcore"
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
	sequence := uint64(0)
	if err == nil && metadata != nil {
		deliveryAttempt = int(metadata.NumDelivered)
		sequence = metadata.Sequence.Stream
	}

	// Log message received with sequence and delivery attempt for debugging
	if metadata != nil {
		logger.Logger.Info("Message received from NATS",
			zap.Uint64("sequence", sequence),
			zap.Int("delivery_attempt", deliveryAttempt),
			zap.Uint64("num_pending", metadata.NumPending),
		)
	} else {
		logger.Logger.Info("Message received from NATS",
			zap.Uint64("sequence", sequence),
			zap.Int("delivery_attempt", deliveryAttempt),
		)
	}

	// Parse event to extract domain and call_id for logging
	var event struct {
		CallID string `json:"call_id"`
		Domain string `json:"domain"` // Required: used for routing
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		logger.Logger.Error("Failed to parse event",
			zap.Error(err),
			zap.Uint64("sequence", sequence),
			zap.Int("delivery_attempt", deliveryAttempt),
		)
		// NAK the message to trigger redelivery
		if err := cs.consumer.Nak(msg); err != nil {
			logger.Logger.Error("Failed to NAK message", zap.Error(err))
		}
		return
	}

	// Validate domain is present
	if event.Domain == "" {
		logger.Logger.Error("Event missing domain field",
			zap.String("call_id", event.CallID),
			zap.Uint64("sequence", sequence),
			zap.Int("delivery_attempt", deliveryAttempt),
		)
		// NAK the message - cannot route without domain
		if err := cs.consumer.Nak(msg); err != nil {
			logger.Logger.Error("Failed to NAK message", zap.Error(err))
		}
		return
	}

	// Log processing start with sequence for tracking
	logger.Logger.Info("Processing message",
		zap.String("call_id", event.CallID),
		zap.String("domain", event.Domain),
		zap.Uint64("sequence", sequence),
		zap.Int("delivery_attempt", deliveryAttempt),
	)

	// Create context with timeout for forwarding
	ctx, cancel := context.WithTimeout(cs.ctx, 3*time.Second)
	defer cancel()

	// Forward event to all endpoints
	err = cs.forwarder.ForwardEvent(ctx, msg.Data, event.Domain, deliveryAttempt)
	if err != nil {
		logger.LogWithDomain(zapcore.ErrorLevel, "Failed to forward event",
			zap.String("call_id", event.CallID),
			zap.String("domain", event.Domain),
			zap.String("state", event.State),
			zap.String("status", event.Status),
			zap.Uint64("sequence", sequence),
			zap.Int("delivery_attempt", deliveryAttempt),
			zap.Error(err),
		)
		// DO NOT acknowledge - let JetStream redeliver after ack_wait expires
		// The message will be redelivered automatically by JetStream
		// This will cause delivery_attempt to increase on next delivery
		logger.Logger.Warn("Message will be redelivered by JetStream",
			zap.String("call_id", event.CallID),
			zap.Uint64("sequence", sequence),
			zap.Int("current_attempt", deliveryAttempt),
		)
		return
	}

	// All endpoints succeeded - acknowledge the message
	if err := cs.consumer.Ack(msg); err != nil {
		logger.Logger.Error("Failed to acknowledge message",
			zap.String("call_id", event.CallID),
			zap.Uint64("sequence", sequence),
			zap.Error(err),
		)
		return
	}

	logger.Logger.Info("Event processed and acknowledged",
		zap.String("call_id", event.CallID),
		zap.String("domain", event.Domain),
		zap.String("state", event.State),
		zap.String("status", event.Status),
		zap.Uint64("sequence", sequence),
		zap.Int("delivery_attempt", deliveryAttempt),
	)
}

// Stop stops the consumer service
func (cs *ConsumerService) Stop() {
	logger.Logger.Info("Stopping consumer service")
	cs.cancel()
}

