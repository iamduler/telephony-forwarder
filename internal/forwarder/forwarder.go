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
	"calleventhub/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Forwarder forwards events to backend endpoints
type Forwarder struct {
	config   *config.Config
	client   *http.Client
	attempts map[string]int // Track delivery attempts for logging
	mu       sync.RWMutex
	store    *store.Store // Store for tracking forwarded events
}

// NewForwarder creates a new forwarder
func NewForwarder(cfg *config.Config, eventStore *store.Store) *Forwarder {
	return &Forwarder{
		config: cfg,
		client: &http.Client{
			Timeout: 3 * time.Second, // Backend timeout: 3 seconds
		},
		attempts: make(map[string]int),
		store:    eventStore,
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
	f.mu.RLock()
	endpoints := f.config.GetEndpoints(domain)
	maxDeliveries := f.config.NATS.MaxDeliveries
	f.mu.RUnlock()
	if len(endpoints) == 0 {
		return fmt.Errorf("no endpoints configured for domain: %s", domain)
	}

	// Parse event to extract all fields for logging
	// This preserves ALL fields from different PBX systems
	var eventMap map[string]interface{}
	if err := json.Unmarshal(eventData, &eventMap); err != nil {
		logger.Logger.Warn("Failed to parse event for logging", zap.Error(err))
		// Fallback: try to extract at least call_id
		var fallbackEvent struct {
			CallID string `json:"call_id"`
		}
		_ = json.Unmarshal(eventData, &fallbackEvent)
		eventMap = map[string]interface{}{
			"call_id": fallbackEvent.CallID,
		}
	}

	// Extract call_id for convenience - support different naming conventions
	callID := ""
	if id, ok := eventMap["call_id"].(string); ok {
		callID = id
	} else if id, ok := eventMap["CallID"].(string); ok {
		callID = id
		eventMap["call_id"] = callID // Normalize to lowercase
	} else if id, ok := eventMap["call_id"].(float64); ok {
		callID = fmt.Sprintf("%.0f", id)
	} else if id, ok := eventMap["CallID"].(float64); ok {
		callID = fmt.Sprintf("%.0f", id)
		eventMap["call_id"] = callID // Normalize to lowercase
	}

	// Add delivery_attempt to event map for logging
	eventMap["delivery_attempt"] = deliveryAttempt

	// Use domain-aware logging with full event data
	// Log call_id and delivery_attempt to help debug duplicate forwarding issues
	logger.LogWithDomain(zapcore.InfoLevel, "Forwarding event",
		zap.String("domain", domain),
		zap.String("call_id", callID),
		zap.Int("delivery_attempt", deliveryAttempt),
		zap.Int("endpoint_count", len(endpoints)),
		zap.Any("event", eventMap), // Log full event data
	)

	// Add delivery_attempt and using_forwarder to event payload
	eventPayload, err := f.enrichPayload(eventData, deliveryAttempt)
	if err != nil {
		logger.Logger.Warn("Failed to enrich payload, using original payload",
			zap.String("call_id", callID),
			zap.Error(err),
		)
		eventPayload = eventData // Fallback to original payload
	}

	// Extract state and status for error logging
	state := ""
	status := ""
	if s, ok := eventMap["state"].(string); ok {
		state = s
	}
	if s, ok := eventMap["status"].(string); ok {
		status = s
	}

	// Forward to all endpoints concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, len(endpoints))

	for _, endpoint := range endpoints {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := f.forwardToEndpoint(ctx, url, eventPayload, callID, domain, state, status); err != nil {
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
		// Create error messages array for logging
		errorMessages := make([]string, len(errors))
		for i, err := range errors {
			errorMessages[i] = err.Error()
		}

		// Log full event data with error information
		logger.LogWithDomain(zapcore.ErrorLevel, "Failed to forward event",
			zap.String("domain", domain),
			zap.Int("failed_endpoints", len(errors)),
			zap.Strings("errors", errorMessages),
			zap.Any("event", eventMap), // Log full event data
		)

		// Store the failed event for dashboard
		if f.store != nil {
			f.store.AddFailedEvent(eventData, domain, callID, deliveryAttempt, maxDeliveries, endpoints, errorMessages)
		}

		return fmt.Errorf("failed to forward to %d endpoint(s): %v", len(errors), errors)
	}

	// Log full event data on success
	logger.LogWithDomain(zapcore.InfoLevel, "Event forwarded successfully",
		zap.String("domain", domain),
		zap.Int("endpoint_count", len(endpoints)),
		zap.Any("event", eventMap), // Log full event data
	)

	// Store the forwarded event for dashboard
	if f.store != nil {
		f.store.AddEvent(eventData, domain, callID, deliveryAttempt, endpoints)
	}

	return nil
}

// ReloadConfig reloads the configuration from the specified file path
func (f *Forwarder) ReloadConfig(configPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Load new config
	newCfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Validate new config
	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("invalid reloaded config: %w", err)
	}

	// Update config atomically
	f.config = newCfg

	logger.Logger.Info("Configuration reloaded successfully",
		zap.Int("route_count", len(newCfg.Routes)),
	)

	return nil
}

// GetConfig returns a copy of the current configuration (for read-only access)
func (f *Forwarder) GetConfig() *config.Config {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.config
}

// enrichPayload adds delivery_attempt and using_forwarder fields to the event payload
func (f *Forwarder) enrichPayload(eventData []byte, deliveryAttempt int) ([]byte, error) {
	// Parse the event as a map to preserve all fields
	var eventMap map[string]interface{}
	if err := json.Unmarshal(eventData, &eventMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	// Add or update delivery_attempt field
	eventMap["delivery_attempt"] = deliveryAttempt

	// Add using_forwarder field to indicate this event is forwarded by the forwarder service
	eventMap["using_forwarder"] = 1

	// Marshal back to JSON
	payload, err := json.Marshal(eventMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}

	return payload, nil
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
