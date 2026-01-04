package store

import (
	"encoding/json"
	"sync"
	"time"
)

// ForwardedEvent represents an event that has been successfully forwarded
type ForwardedEvent struct {
	Event         json.RawMessage `json:"event"`
	Domain        string          `json:"domain"`
	CallID        string          `json:"call_id"`
	ForwardedAt   time.Time       `json:"forwarded_at"`
	DeliveryAttempt int           `json:"delivery_attempt"`
	Endpoints     []string        `json:"endpoints"`
}

// FailedEvent represents an event that failed to forward
type FailedEvent struct {
	Event         json.RawMessage `json:"event"`
	Domain        string          `json:"domain"`
	CallID        string          `json:"call_id"`
	FailedAt      time.Time       `json:"failed_at"`
	DeliveryAttempt int           `json:"delivery_attempt"`
	MaxDeliveries int            `json:"max_deliveries"`
	Endpoints     []string        `json:"endpoints"`
	ErrorMessages []string        `json:"error_messages"`
	WillRetry     bool            `json:"will_retry"` // true if delivery_attempt < max_deliveries
}

// Store holds forwarded events in memory
type Store struct {
	successfulEvents []ForwardedEvent
	failedEvents     []FailedEvent
	mu               sync.RWMutex
	maxSize          int // Maximum number of events to keep (0 = unlimited)
}

// NewStore creates a new event store
func NewStore(maxSize int) *Store {
	return &Store{
		successfulEvents: make([]ForwardedEvent, 0),
		failedEvents:     make([]FailedEvent, 0),
		maxSize:          maxSize,
	}
}

// AddEvent adds a successfully forwarded event to the store
func (s *Store) AddEvent(event json.RawMessage, domain, callID string, deliveryAttempt int, endpoints []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	forwardedEvent := ForwardedEvent{
		Event:          event,
		Domain:         domain,
		CallID:         callID,
		ForwardedAt:    time.Now(),
		DeliveryAttempt: deliveryAttempt,
		Endpoints:      endpoints,
	}

	s.successfulEvents = append(s.successfulEvents, forwardedEvent)

	// Limit size if maxSize is set
	if s.maxSize > 0 && len(s.successfulEvents) > s.maxSize {
		// Remove oldest events
		removeCount := len(s.successfulEvents) - s.maxSize
		s.successfulEvents = s.successfulEvents[removeCount:]
	}
}

// AddFailedEvent adds a failed event to the store
func (s *Store) AddFailedEvent(event json.RawMessage, domain, callID string, deliveryAttempt, maxDeliveries int, endpoints []string, errorMessages []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	failedEvent := FailedEvent{
		Event:          event,
		Domain:         domain,
		CallID:         callID,
		FailedAt:       time.Now(),
		DeliveryAttempt: deliveryAttempt,
		MaxDeliveries:  maxDeliveries,
		Endpoints:      endpoints,
		ErrorMessages:  errorMessages,
		WillRetry:      deliveryAttempt < maxDeliveries,
	}

	s.failedEvents = append(s.failedEvents, failedEvent)

	// Limit size if maxSize is set
	if s.maxSize > 0 && len(s.failedEvents) > s.maxSize {
		// Remove oldest events
		removeCount := len(s.failedEvents) - s.maxSize
		s.failedEvents = s.failedEvents[removeCount:]
	}
}

// GetEventsByDomain returns all successful events grouped by domain
func (s *Store) GetEventsByDomain() map[string][]ForwardedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]ForwardedEvent)
	for _, event := range s.successfulEvents {
		result[event.Domain] = append(result[event.Domain], event)
	}

	return result
}

// GetFailedEventsByDomain returns all failed events grouped by domain
func (s *Store) GetFailedEventsByDomain() map[string][]FailedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]FailedEvent)
	for _, event := range s.failedEvents {
		result[event.Domain] = append(result[event.Domain], event)
	}

	return result
}

// GetEvents returns all successful events (for API)
func (s *Store) GetEvents() []ForwardedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]ForwardedEvent, len(s.successfulEvents))
	copy(result, s.successfulEvents)
	return result
}

// GetFailedEvents returns all failed events (for API)
func (s *Store) GetFailedEvents() []FailedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]FailedEvent, len(s.failedEvents))
	copy(result, s.failedEvents)
	return result
}

// GetEventsByDomainFiltered returns successful events filtered by domain
func (s *Store) GetEventsByDomainFiltered(domain string) []ForwardedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ForwardedEvent
	for _, event := range s.successfulEvents {
		if event.Domain == domain {
			result = append(result, event)
		}
	}
	return result
}

// GetFailedEventsByDomainFiltered returns failed events filtered by domain
func (s *Store) GetFailedEventsByDomainFiltered(domain string) []FailedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []FailedEvent
	for _, event := range s.failedEvents {
		if event.Domain == domain {
			result = append(result, event)
		}
	}
	return result
}

// GetStats returns statistics about forwarded events
func (s *Store) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	successfulDomainCount := make(map[string]int)
	failedDomainCount := make(map[string]int)
	totalSuccessful := len(s.successfulEvents)
	totalFailed := len(s.failedEvents)

	for _, event := range s.successfulEvents {
		successfulDomainCount[event.Domain]++
	}

	for _, event := range s.failedEvents {
		failedDomainCount[event.Domain]++
	}

	// Count retry attempts
	retryCount := 0
	for _, event := range s.failedEvents {
		if event.WillRetry {
			retryCount++
		}
	}

	return map[string]interface{}{
		"total_successful":      totalSuccessful,
		"total_failed":           totalFailed,
		"total_events":           totalSuccessful + totalFailed,
		"retry_count":            retryCount,
		"successful_domain_count": successfulDomainCount,
		"failed_domain_count":    failedDomainCount,
		"domains":               len(successfulDomainCount) + len(failedDomainCount),
	}
}

