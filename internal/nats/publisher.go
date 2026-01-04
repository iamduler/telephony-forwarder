package nats

import (
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"calleventhub/internal/logger"
)

// Publisher handles publishing events to NATS JetStream
type Publisher struct {
	conn       *nats.Conn
	js         nats.JetStreamContext
	subject    string
	streamName string
	connected  bool
}

// NewPublisher creates a new NATS publisher
func NewPublisher(url, streamName, subjectPattern string) (*Publisher, error) {
	opts := []nats.Option{
		nats.Name("event-hub-publisher"),
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
	if err == nats.ErrStreamNotFound {
		// Create stream if it doesn't exist
		_, err = js.AddStream(&nats.StreamConfig{
			Name:      streamName,
			Subjects:  []string{subjectPattern},
			Retention: nats.LimitsPolicy,
			MaxAge:    24 * time.Hour,
		})
		if err != nil {
			conn.Close()
			return nil, err
		}
		logger.Logger.Info("Created NATS stream", zap.String("stream", streamName))
	} else if err != nil {
		conn.Close()
		return nil, err
	}

	// Convert pattern to specific subject for publishing
	// Pattern "call.signal.*" -> subject "call.signal.events"
	publishSubject := "call.signal.events"
	if subjectPattern == "call.signal.*" {
		publishSubject = "call.signal.events"
	} else {
		// For other patterns, try to derive a subject
		// Replace * with a default value
		publishSubject = subjectPattern
	}

	pub := &Publisher{
		conn:       conn,
		js:         js,
		subject:    publishSubject,
		streamName: streamName,
		connected:  true,
	}

	// Monitor connection status
	go pub.monitorConnection()

	return pub, nil
}

// monitorConnection monitors the NATS connection status
func (p *Publisher) monitorConnection() {
	for {
		if !p.conn.IsConnected() {
			p.connected = false
		} else {
			p.connected = true
		}
		time.Sleep(1 * time.Second)
	}
}

// Publish publishes an event to NATS JetStream
func (p *Publisher) Publish(data []byte) error {
	_, err := p.js.Publish(p.subject, data)
	return err
}

// IsConnected returns whether the NATS connection is alive
func (p *Publisher) IsConnected() bool {
	return p.conn.IsConnected() && p.connected
}

// Close closes the NATS connection
func (p *Publisher) Close() {
	if p.conn != nil {
		p.conn.Close()
	}
}

// GetJetStream returns the JetStream context (for reading messages)
func (p *Publisher) GetJetStream() nats.JetStreamContext {
	return p.js
}

// GetStreamName returns the stream name
func (p *Publisher) GetStreamName() string {
	return p.streamName
}

