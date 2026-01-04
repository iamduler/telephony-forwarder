package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"calleventhub/internal/config"
	"calleventhub/internal/logger"

	"go.uber.org/zap"
)

// Forwarder forwards events to backend endpoints
type Forwarder struct {
	config   *config.Config
	client   *http.Client
	attempts map[string]int // Track delivery attempts for logging
	mu       sync.RWMutex
}

// NewForwarder creates a new forwarder
func NewForwarder(cfg *config.Config) *Forwarder {
	return &Forwarder{
		config: cfg,
		client: &http.Client{
			Timeout: 3 * time.Second, // Backend timeout: 3 seconds
		},
		attempts: make(map[string]int),
	}
}

// ForwardEvent forwards an event to all configured endpoints for the domain
// 
// Behavior:
// - Forwards to ALL endpoints concurrently (parallel HTTP requests)
// - If ANY endpoint fails (non-2xx response or timeout), returns error
// - The caller should NOT acknowledge the JetStream message if this returns an error
// - JetStream will redeliver the entire message after ack_wait expires
// - Backend endpoints MUST be idempotent based on call_id
func (f *Forwarder) ForwardEvent(ctx context.Context, eventData []byte, domain string, deliveryAttempt int) error {
	endpoints := f.config.GetEndpoints(domain)
	if len(endpoints) == 0 {
		return fmt.Errorf("no endpoints configured for domain: %s", domain)
	}

	// Parse event to extract call_id for logging
	var event struct {
		CallID string `json:"call_id"`
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(eventData, &event); err != nil {
		logger.Logger.Warn("Failed to parse event for logging", zap.Error(err))
	}

	callID := event.CallID

	logger.Logger.Info("Forwarding event",
		zap.String("call_id", callID),
		zap.String("domain", domain),
		zap.String("state", event.State),
		zap.String("status", event.Status),
		zap.Int("delivery_attempt", deliveryAttempt),
		zap.Int("endpoint_count", len(endpoints)),
	)

	// Forward to all endpoints concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, len(endpoints))

		for _, endpoint := range endpoints {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := f.forwardToEndpoint(ctx, url, eventData, callID, domain, event.State, event.Status); err != nil {
				errChan <- fmt.Errorf("endpoint %s failed: %w", url, err)
			}
		}(endpoint)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check if any endpoint failed
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		logger.Logger.Error("Event forwarding failed",
			zap.String("call_id", callID),
			zap.String("domain", domain),
			zap.String("state", event.State),
			zap.String("status", event.Status),
			zap.Int("delivery_attempt", deliveryAttempt),
			zap.Int("failed_endpoints", len(errors)),
			zap.Any("errors", errors),
		)
		return fmt.Errorf("failed to forward to %d endpoint(s): %v", len(errors), errors)
	}

	logger.Logger.Info("Event forwarded successfully",
		zap.String("call_id", callID),
		zap.String("domain", domain),
		zap.String("state", event.State),
		zap.String("status", event.Status),
		zap.Int("delivery_attempt", deliveryAttempt),
		zap.Int("endpoint_count", len(endpoints)),
	)

	return nil
}

// forwardToEndpoint forwards the event to a single endpoint
func (f *Forwarder) forwardToEndpoint(ctx context.Context, url string, eventData []byte, callID, domain, state, status string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(eventData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Call-ID", callID)
	req.Header.Set("X-Domain", domain)

	resp, err := f.client.Do(req)
	if err != nil {
		logger.Logger.Warn("HTTP request failed",
			zap.String("call_id", callID),
			zap.String("domain", domain),
			zap.String("state", state),
			zap.String("status", status),
			zap.String("endpoint", url),
			zap.Error(err),
		)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("non-2xx response: %d", resp.StatusCode)
		logger.Logger.Warn("HTTP request returned non-2xx",
			zap.String("call_id", callID),
			zap.String("domain", domain),
			zap.String("state", state),
			zap.String("status", status),
			zap.String("endpoint", url),
			zap.Int("status_code", resp.StatusCode),
		)
		return err
	}

	return nil
}

