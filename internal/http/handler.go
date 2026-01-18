package http

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"calleventhub/internal/config"
	"calleventhub/internal/forwarder"
	"calleventhub/internal/logger"
	"calleventhub/internal/nats"
	"calleventhub/internal/store"

	natsgo "github.com/nats-io/nats.go"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

//go:embed web/*
var webAssets embed.FS

// Event represents a subset of common fields in telephony signaling events
// Note: The system preserves ALL fields from incoming JSON, not just these.
// Different PBX systems may have different field structures and naming conventions.
// The handler decodes JSON to map[string]interface{} to preserve all fields.
type Event struct {
	ActualHotline        string `json:"actual_hotline"`
	Billsec              string `json:"billsec"`
	CallID               string `json:"call_id"`
	CRMContactID         string `json:"crm_contact_id"`
	Direction            string `json:"direction"`
	Domain               string `json:"domain"` // Required: used for routing
	Duration             string `json:"duration"`
	FromNumber           string `json:"from_number"`
	Hotline              string `json:"hotline"`
	Network              string `json:"network"`
	Provider             string `json:"provider"`
	ReceiveDest          string `json:"receive_dest"`
	SIPCallID            string `json:"sip_call_id"`
	SIPHangupDisposition string `json:"sip_hangup_disposition"`
	State                string `json:"state"`
	Status               string `json:"status"`
	TimeEnded            string `json:"time_ended"`
	TimeStarted          string `json:"time_started"`
	ToNumber             string `json:"to_number"`
}

// Handler handles HTTP requests
type Handler struct {
	publisher  *nats.Publisher
	store      *store.Store
	config     *config.Config
	forwarder  *forwarder.Forwarder
	configPath string
}

// NewHandler creates a new HTTP handler
func NewHandler(publisher *nats.Publisher, eventStore *store.Store, cfg *config.Config, fwd *forwarder.Forwarder, configPath string) *Handler {
	return &Handler{
		publisher:  publisher,
		store:      eventStore,
		config:     cfg,
		forwarder:  fwd,
		configPath: configPath,
	}
}

// HandleEvents handles POST /events
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Decode JSON directly to map to preserve ALL fields from different PBX systems
	var eventMap map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&eventMap); err != nil {
		logger.Logger.Warn("Failed to decode event", zap.Error(err))
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate required fields
	// Domain is required for routing
	domain, ok := eventMap["domain"].(string)
	if !ok || domain == "" {
		// Try alternative field names that might be used by different PBX systems
		if altDomain, ok := eventMap["Domain"].(string); ok && altDomain != "" {
			domain = altDomain
			eventMap["domain"] = domain // Normalize to lowercase
		} else {
			http.Error(w, "domain is required", http.StatusBadRequest)
			return
		}
	}

	// Extract call_id for logging (if available)
	callID := ""
	if id, ok := eventMap["call_id"].(string); ok {
		callID = id
	} else if id, ok := eventMap["CallID"].(string); ok {
		callID = id
		eventMap["call_id"] = callID // Normalize to lowercase
	}

	// Publish to NATS JetStream - preserve all fields
	eventJSON, err := json.Marshal(eventMap)
	if err != nil {
		logger.Logger.Error("Failed to marshal event", zap.Error(err), zap.String("call_id", callID), zap.String("domain", domain))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.publisher.Publish(eventJSON); err != nil {
		logger.Logger.Error("Failed to publish event", zap.Error(err), zap.String("call_id", callID), zap.String("domain", domain))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Log full event data with all fields from any PBX system
	// This log is written BEFORE forwarding, so you can check how many events
	// were actually received from the PBX system
	// IMPORTANT: If you see multiple "Event received and published" logs for the same call_id,
	// it means the PBX is sending the same event multiple times, NOT that the app is duplicating it
	logger.LogWithDomain(zapcore.InfoLevel, "Event received and published",
		zap.String("call_id", callID),
		zap.String("domain", domain),
		zap.String("state", getStringFromMap(eventMap, "state")),
		zap.String("status", getStringFromMap(eventMap, "status")),
		zap.Any("event", eventMap), // Log full event data with all fields
	)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// HandleHealth handles GET /health
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check NATS connection
	if !h.publisher.IsConnected() {
		http.Error(w, "NATS not connected", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"healthy"}`))
}

// HandleGetEvents handles GET /api/events - returns events grouped by domain
func (h *Handler) HandleGetEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.store == nil {
		http.Error(w, "Event store not available", http.StatusInternalServerError)
		return
	}

	// Get domain filter and event type from query parameters
	domain := r.URL.Query().Get("domain")
	eventType := r.URL.Query().Get("type") // "success", "failed", or "" for all

	var eventsByDomain map[string][]store.ForwardedEvent
	var failedEventsByDomain map[string][]store.FailedEvent

	if domain != "" {
		// Filter by specific domain
		if eventType != "failed" {
			events := h.store.GetEventsByDomainFiltered(domain)
			eventsByDomain = map[string][]store.ForwardedEvent{
				domain: events,
			}
		}
		if eventType != "success" {
			failedEvents := h.store.GetFailedEventsByDomainFiltered(domain)
			failedEventsByDomain = map[string][]store.FailedEvent{
				domain: failedEvents,
			}
		}
	} else {
		// Get all events grouped by domain
		if eventType != "failed" {
			eventsByDomain = h.store.GetEventsByDomain()
		}
		if eventType != "success" {
			failedEventsByDomain = h.store.GetFailedEventsByDomain()
		}
	}

	// Sort events by timestamp (newest first) for each domain
	for domain := range eventsByDomain {
		sort.Slice(eventsByDomain[domain], func(i, j int) bool {
			return eventsByDomain[domain][i].ForwardedAt.After(eventsByDomain[domain][j].ForwardedAt)
		})
	}
	for domain := range failedEventsByDomain {
		sort.Slice(failedEventsByDomain[domain], func(i, j int) bool {
			return failedEventsByDomain[domain][i].FailedAt.After(failedEventsByDomain[domain][j].FailedAt)
		})
	}

	// Get stats
	stats := h.store.GetStats()

	response := map[string]interface{}{
		"events_by_domain":        eventsByDomain,
		"failed_events_by_domain": failedEventsByDomain,
		"stats":                   stats,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// HandleGetStats handles GET /api/stats - returns statistics
func (h *Handler) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.store == nil {
		http.Error(w, "Event store not available", http.StatusInternalServerError)
		return
	}

	stats := h.store.GetStats()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// StreamMessage represents a message in the NATS stream
type StreamMessage struct {
	Sequence     uint64                 `json:"sequence"`
	Timestamp    time.Time              `json:"timestamp"`
	Subject      string                 `json:"subject"`
	Data         json.RawMessage        `json:"data"`
	EventSummary map[string]interface{} `json:"event_summary,omitempty"`
}

// HandleGetStreamMessages handles GET /api/stream/messages - returns messages from NATS stream
func (h *Handler) HandleGetStreamMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.publisher == nil {
		http.Error(w, "NATS publisher not available", http.StatusInternalServerError)
		return
	}

	// Get query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 100 // default
	if limitStr != "" {
		if parsed, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil || parsed != 1 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000 // max limit
		}
		if limit < 1 {
			limit = 1
		}
	}

	js := h.publisher.GetJetStream()
	streamName := h.publisher.GetStreamName()

	// Get stream info
	streamInfo, err := js.StreamInfo(streamName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get stream info: %v", err), http.StatusInternalServerError)
		return
	}

	// Get subject pattern from stream config
	// Use the first subject from stream config, or default pattern
	subjectPattern := "call.signal.*"
	if len(streamInfo.Config.Subjects) > 0 {
		subjectPattern = streamInfo.Config.Subjects[0]
	}

	// Create a temporary consumer to read messages
	// Use DeliverAllPolicy to get all messages
	consumerName := fmt.Sprintf("temp-reader-%d", time.Now().Unix())
	consumerConfig := &natsgo.ConsumerConfig{
		Name:          consumerName,
		DeliverPolicy: natsgo.DeliverAllPolicy, // Read all messages
		AckPolicy:     natsgo.AckNonePolicy,    // Don't need to ack for reading
		MaxDeliver:    1,                       // Only deliver once
	}

	_, err = js.AddConsumer(streamName, consumerConfig)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create consumer: %v", err), http.StatusInternalServerError)
		return
	}
	defer js.DeleteConsumer(streamName, consumerName) // Clean up

	// Subscribe to read messages using subject pattern
	sub, err := js.PullSubscribe(subjectPattern, consumerName, natsgo.ManualAck())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to subscribe: %v", err), http.StatusInternalServerError)
		return
	}
	defer sub.Unsubscribe()

	// Fetch messages
	msgs, err := sub.Fetch(limit, natsgo.MaxWait(2*time.Second))
	if err != nil && err != natsgo.ErrTimeout {
		http.Error(w, fmt.Sprintf("Failed to fetch messages: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert messages to response format
	result := make([]StreamMessage, 0, len(msgs))
	for _, msg := range msgs {
		metadata, _ := msg.Metadata()

		streamMsg := StreamMessage{
			Sequence:  metadata.Sequence.Stream,
			Timestamp: metadata.Timestamp,
			Subject:   msg.Subject,
			Data:      msg.Data,
		}

		// Try to parse event data for summary
		var eventData map[string]interface{}
		if err := json.Unmarshal(msg.Data, &eventData); err == nil {
			streamMsg.EventSummary = map[string]interface{}{
				"call_id": eventData["call_id"],
				"domain":  eventData["domain"],
				"state":   eventData["state"],
				"status":  eventData["status"],
			}
		}

		result = append(result, streamMsg)
	}

	response := map[string]interface{}{
		"stream_name":    streamName,
		"total_messages": streamInfo.State.Msgs,
		"messages":       result,
		"count":          len(result),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Server wraps the HTTP server
type Server struct {
	httpServer *http.Server
	handler    *Handler
}

// NewServer creates a new HTTP server
func NewServer(port int, handler *Handler) *Server {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/events", handler.HandleEvents)
	mux.HandleFunc("/health", handler.HandleHealth)
	mux.HandleFunc("/api/events", handler.HandleGetEvents)
	mux.HandleFunc("/api/stats", handler.HandleGetStats)
	mux.HandleFunc("/api/stream/messages", handler.HandleGetStreamMessages)
	mux.HandleFunc("/api/logs", handler.HandleGetLogs)
	mux.HandleFunc("/api/logs/domains", handler.HandleGetLogDomains)
	mux.HandleFunc("/api/config", handler.HandleGetConfig)
	mux.HandleFunc("/api/config/domains", handler.HandleGetConfigDomains)
	mux.HandleFunc("/api/config/reload", handler.HandleReloadConfig)

	// Serve static assets (JS, CSS, etc.)
	mux.HandleFunc("/static/", handler.HandleStatic)

	// Serve log viewer
	mux.HandleFunc("/logs", handler.HandleLogsViewer)

	// Serve config viewer
	mux.HandleFunc("/config", handler.HandleConfigViewer)

	// Serve dashboard (must be last to catch all other routes)
	mux.HandleFunc("/", handler.HandleDashboard)

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		handler: handler,
	}
}

// HandleDashboard serves the dashboard HTML page
func (h *Handler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Read embedded HTML file
	htmlFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Logger.Error("Failed to read dashboard HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	htmlContent, err := fs.ReadFile(htmlFS, "dashboard.html")
	if err != nil {
		logger.Logger.Error("Failed to read dashboard HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(htmlContent)
}

// HandleLogsViewer serves the log viewer HTML page
func (h *Handler) HandleLogsViewer(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/logs" {
		http.NotFound(w, r)
		return
	}

	// Read embedded HTML file
	htmlFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Logger.Error("Failed to read logs viewer HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	htmlContent, err := fs.ReadFile(htmlFS, "logs.html")
	if err != nil {
		logger.Logger.Error("Failed to read logs viewer HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(htmlContent)
}

// HandleConfigViewer serves the config viewer HTML page
func (h *Handler) HandleConfigViewer(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/config" {
		http.NotFound(w, r)
		return
	}

	// Read embedded HTML file
	htmlFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Logger.Error("Failed to read config viewer HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	htmlContent, err := fs.ReadFile(htmlFS, "config.html")
	if err != nil {
		logger.Logger.Error("Failed to read config viewer HTML", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(htmlContent)
}

// HandleStatic serves static assets (JS, CSS, etc.)
func (h *Handler) HandleStatic(w http.ResponseWriter, r *http.Request) {
	// Extract filename from path (e.g., /static/dashboard.js -> dashboard.js)
	filename := strings.TrimPrefix(r.URL.Path, "/static/")
	if filename == "" || strings.Contains(filename, "..") {
		http.NotFound(w, r)
		return
	}

	// Read embedded file
	webFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Logger.Error("Failed to read web assets", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	fileContent, err := fs.ReadFile(webFS, filename)
	if err != nil {
		logger.Logger.Debug("Static file not found", zap.String("file", filename), zap.Error(err))
		http.NotFound(w, r)
		return
	}

	// Set appropriate Content-Type based on file extension
	if strings.HasSuffix(filename, ".js") {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	} else if strings.HasSuffix(filename, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}

	w.WriteHeader(http.StatusOK)
	w.Write(fileContent)
}

// LogEntry represents a parsed log entry from log files
type LogEntry struct {
	Timestamp       string                 `json:"timestamp"`
	Level           string                 `json:"level"`
	Message         string                 `json:"msg"`
	CallID          string                 `json:"call_id,omitempty"`
	Domain          string                 `json:"domain,omitempty"`
	State           string                 `json:"state,omitempty"`
	Status          string                 `json:"status,omitempty"`
	Direction       string                 `json:"direction,omitempty"`
	Error           string                 `json:"error,omitempty"`
	DeliveryAttempt int                    `json:"delivery_attempt,omitempty"`
	Fields          map[string]interface{} `json:"-"`
}

// HandleGetLogs handles GET /api/logs - reads logs from log files
func (h *Handler) HandleGetLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get query parameters
	domain := r.URL.Query().Get("domain")
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	logsDir := "logs"
	if domain == "" {
		// List all domains
		domains, err := h.listLogDomains(logsDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list domains: %v", err), http.StatusInternalServerError)
			return
		}

		response := map[string]interface{}{
			"domains": domains,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Read logs for specific domain and date
	logs, err := h.readLogsFromFile(logsDir, domain, date)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read logs: %v", err), http.StatusInternalServerError)
		return
	}

	// Group logs by domain
	logsByDomain := make(map[string][]LogEntry)
	for _, log := range logs {
		if log.Domain != "" {
			logsByDomain[log.Domain] = append(logsByDomain[log.Domain], log)
		}
	}

	// Extract events from logs (look for "Event forwarded successfully" and "Failed to forward event")
	// We need to track "Event received and published" entries to get direction and other fields
	eventsByDomain := make(map[string][]map[string]interface{})
	failedEventsByDomain := make(map[string][]map[string]interface{})

	// Track event metadata by call_id to enrich failed events
	eventMetadata := make(map[string]map[string]interface{}) // call_id -> metadata

	maxDeliveries := 3 // Default value
	if h.config != nil {
		maxDeliveries = h.config.NATS.MaxDeliveries
	}

	for domain, entries := range logsByDomain {
		// First pass: collect full event data from "Event received and published"
		for _, entry := range entries {
			if entry.Message == "Event received and published" {
				// Extract full event data from Fields
				if entry.Fields != nil {
					if eventData, ok := entry.Fields["event"].(map[string]interface{}); ok {
						eventMetadata[entry.CallID] = eventData
					}
				}
			}
		}

		// Second pass: extract events
		for _, entry := range entries {
			// Extract full event data from Fields map (contains "event" field with all data)
			var fullEventData map[string]interface{}
			if entry.Fields != nil {
				if eventData, ok := entry.Fields["event"].(map[string]interface{}); ok {
					// Use the full event data from log
					fullEventData = eventData
				} else {
					// Fallback: use Fields directly if "event" field doesn't exist
					fullEventData = entry.Fields
				}
			}

			// Convert timestamp to local timezone
			timestamp := entry.Timestamp
			var parsedTime time.Time
			var err error

			// Try different timestamp formats
			formats := []string{
				time.RFC3339,                    // 2006-01-02T15:04:05Z07:00
				time.RFC3339Nano,                // 2006-01-02T15:04:05.999999999Z07:00
				"2006-01-02T15:04:05.000Z07:00", // Custom format with milliseconds
				"2006-01-02T15:04:05Z",          // UTC without timezone offset
			}

			for _, format := range formats {
				parsedTime, err = time.Parse(format, timestamp)
				if err == nil {
					break
				}
			}

			if err == nil {
				// Convert to local timezone and format with timezone offset
				timestamp = parsedTime.Local().Format("2006-01-02T15:04:05.000Z07:00")
			}

			if entry.Message == "Event forwarded successfully" || entry.Message == "Forwarding event" {
				deliveryAttempt := 1
				if entry.DeliveryAttempt > 0 {
					deliveryAttempt = entry.DeliveryAttempt
				} else if fullEventData != nil {
					if da, ok := fullEventData["delivery_attempt"].(float64); ok {
						deliveryAttempt = int(da)
					} else if da, ok := fullEventData["delivery_attempt"].(int); ok {
						deliveryAttempt = da
					}
				}

				// Use full event data if available, otherwise construct from entry fields
				if fullEventData != nil {
					// Add timestamp, delivery_attempt, and message to full event data
					fullEventData["timestamp"] = timestamp
					fullEventData["forwarded_at"] = timestamp
					fullEventData["delivery_attempt"] = deliveryAttempt
					fullEventData["msg"] = entry.Message
					eventsByDomain[domain] = append(eventsByDomain[domain], fullEventData)
				} else {
					// Fallback: construct from entry fields
					event := map[string]interface{}{
						"call_id":          entry.CallID,
						"domain":           entry.Domain,
						"state":            entry.State,
						"status":           entry.Status,
						"direction":        entry.Direction,
						"forwarded_at":     timestamp,
						"delivery_attempt": deliveryAttempt,
						"msg":              entry.Message,
					}
					eventsByDomain[domain] = append(eventsByDomain[domain], event)
				}
			} else if entry.Message == "Event forwarding failed" || entry.Message == "Failed to forward event" {
				deliveryAttempt := 1
				if entry.DeliveryAttempt > 0 {
					deliveryAttempt = entry.DeliveryAttempt
				} else if fullEventData != nil {
					if da, ok := fullEventData["delivery_attempt"].(float64); ok {
						deliveryAttempt = int(da)
					} else if da, ok := fullEventData["delivery_attempt"].(int); ok {
						deliveryAttempt = da
					}
				}

				// Use full event data if available, otherwise construct from entry fields
				if fullEventData != nil {
					// Add timestamp, error info, delivery_attempt, and message to full event data
					fullEventData["timestamp"] = timestamp
					fullEventData["failed_at"] = timestamp
					fullEventData["delivery_attempt"] = deliveryAttempt
					fullEventData["max_deliveries"] = maxDeliveries
					fullEventData["will_retry"] = deliveryAttempt < maxDeliveries
					fullEventData["msg"] = entry.Message
					if entry.Error != "" {
						fullEventData["error"] = entry.Error
					}
					// Extract error messages from Fields if available
					if entry.Fields != nil {
						if errors, ok := entry.Fields["errors"].([]interface{}); ok {
							fullEventData["error_messages"] = errors
						}
					}
					failedEventsByDomain[domain] = append(failedEventsByDomain[domain], fullEventData)
				} else {
					// Fallback: construct from entry fields
					event := map[string]interface{}{
						"call_id":          entry.CallID,
						"domain":           entry.Domain,
						"state":            entry.State,
						"status":           entry.Status,
						"direction":        entry.Direction,
						"failed_at":        timestamp,
						"error":            entry.Error,
						"delivery_attempt": deliveryAttempt,
						"max_deliveries":   maxDeliveries,
						"will_retry":       deliveryAttempt < maxDeliveries,
					}
					failedEventsByDomain[domain] = append(failedEventsByDomain[domain], event)
				}
			}
		}
	}

	// Sort events by timestamp (newest first) for each domain
	for domain := range eventsByDomain {
		sort.Slice(eventsByDomain[domain], func(i, j int) bool {
			tsI := getTimestampFromEvent(eventsByDomain[domain][i])
			tsJ := getTimestampFromEvent(eventsByDomain[domain][j])
			return tsI.After(tsJ) // Newest first
		})
	}
	for domain := range failedEventsByDomain {
		sort.Slice(failedEventsByDomain[domain], func(i, j int) bool {
			tsI := getTimestampFromEvent(failedEventsByDomain[domain][i])
			tsJ := getTimestampFromEvent(failedEventsByDomain[domain][j])
			return tsI.After(tsJ) // Newest first
		})
	}

	// Calculate stats
	totalSuccessful := 0
	totalFailed := 0
	for _, events := range eventsByDomain {
		totalSuccessful += len(events)
	}
	for _, events := range failedEventsByDomain {
		totalFailed += len(events)
	}

	response := map[string]interface{}{
		"events_by_domain":        eventsByDomain,
		"failed_events_by_domain": failedEventsByDomain,
		"stats": map[string]interface{}{
			"total_successful": totalSuccessful,
			"total_failed":     totalFailed,
			"total_events":     totalSuccessful + totalFailed,
			"domains":          len(logsByDomain),
		},
		"date":   date,
		"domain": domain,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// HandleGetLogDomains handles GET /api/logs/domains - lists available domains in logs
func (h *Handler) HandleGetLogDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logsDir := "logs"
	domains, err := h.listLogDomains(logsDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list domains: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"domains": domains,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// HandleGetConfig handles GET /api/config - returns current route configuration
func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.config == nil {
		http.Error(w, "Configuration not available", http.StatusInternalServerError)
		return
	}

	// Get current config from forwarder (may have been reloaded)
	var routes []config.Route
	if h.forwarder != nil {
		cfg := h.forwarder.GetConfig()
		if cfg != nil {
			routes = cfg.Routes
		} else {
			routes = h.config.Routes
		}
	} else {
		routes = h.config.Routes
	}

	// Build response with routes
	response := map[string]interface{}{
		"routes": routes,
		"count":  len(routes),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// HandleGetConfigDomains handles GET /api/config/domains - returns list of domains from config
func (h *Handler) HandleGetConfigDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.config == nil {
		http.Error(w, "Configuration not available", http.StatusInternalServerError)
		return
	}

	// Get current config from forwarder (may have been reloaded)
	var routes []config.Route
	if h.forwarder != nil {
		cfg := h.forwarder.GetConfig()
		if cfg != nil {
			routes = cfg.Routes
		} else {
			routes = h.config.Routes
		}
	} else {
		routes = h.config.Routes
	}

	// Extract unique domains
	domainMap := make(map[string]bool)
	domains := []string{}
	for _, route := range routes {
		if route.Domain != "" && !domainMap[route.Domain] {
			domainMap[route.Domain] = true
			domains = append(domains, route.Domain)
		}
	}

	// Sort domains alphabetically
	sort.Strings(domains)

	response := map[string]interface{}{
		"domains": domains,
		"count":   len(domains),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// HandleReloadConfig handles POST /api/config/reload - reloads configuration from file
func (h *Handler) HandleReloadConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.forwarder == nil {
		http.Error(w, "Forwarder not available", http.StatusInternalServerError)
		return
	}

	if h.configPath == "" {
		http.Error(w, "Config path not configured", http.StatusInternalServerError)
		return
	}

	// Reload config
	if err := h.forwarder.ReloadConfig(h.configPath); err != nil {
		logger.Logger.Error("Failed to reload config", zap.Error(err))
		http.Error(w, fmt.Sprintf("Failed to reload config: %v", err), http.StatusInternalServerError)
		return
	}

	// Update handler's config reference
	h.config = h.forwarder.GetConfig()

	response := map[string]interface{}{
		"status":  "success",
		"message": "Configuration reloaded successfully",
		"routes":  len(h.config.Routes),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// UpdateConfig updates the handler's config reference (used by file watcher)
func (h *Handler) UpdateConfig(cfg *config.Config) {
	h.config = cfg
}

// listLogDomains lists all domains that have log files
func (h *Handler) listLogDomains(logsDir string) ([]map[string]interface{}, error) {
	var domains []map[string]interface{}

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return domains, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		domainDir := filepath.Join(logsDir, entry.Name())
		logFiles, err := os.ReadDir(domainDir)
		if err != nil {
			continue
		}

		// Count log files and get latest date
		var dates []string
		for _, logFile := range logFiles {
			if !logFile.IsDir() && strings.HasSuffix(logFile.Name(), ".log") {
				date := strings.TrimSuffix(logFile.Name(), ".log")
				if len(date) == 10 { // YYYY-MM-DD format
					dates = append(dates, date)
				}
			}
		}

		if len(dates) > 0 {
			sort.Sort(sort.Reverse(sort.StringSlice(dates)))
			domains = append(domains, map[string]interface{}{
				"domain":      entry.Name(),
				"log_count":   len(dates),
				"latest_date": dates[0],
				"dates":       dates,
			})
		}
	}

	return domains, nil
}

// readLogsFromFile reads logs from a specific domain and date log file
func (h *Handler) readLogsFromFile(logsDir, domain, date string) ([]LogEntry, error) {
	// Sanitize domain name (same as logger does)
	safeDomain := sanitizeDomain(domain)
	logFile := filepath.Join(logsDir, safeDomain, fmt.Sprintf("%s.log", date))

	file, err := os.Open(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []LogEntry{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var logs []LogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip invalid JSON lines
			continue
		}

		// Parse additional fields
		var rawData map[string]interface{}
		if err := json.Unmarshal(line, &rawData); err == nil {
			entry.Fields = rawData
			// Extract common fields
			if callID, ok := rawData["call_id"].(string); ok {
				entry.CallID = callID
			}
			if domain, ok := rawData["domain"].(string); ok {
				entry.Domain = domain
			}
			if state, ok := rawData["state"].(string); ok {
				entry.State = state
			}
			if status, ok := rawData["status"].(string); ok {
				entry.Status = status
			}
			if direction, ok := rawData["direction"].(string); ok {
				entry.Direction = direction
			}
			if errMsg, ok := rawData["error"].(string); ok {
				entry.Error = errMsg
			}
			// Extract delivery_attempt (can be int or float64 from JSON)
			if da, ok := rawData["delivery_attempt"]; ok {
				switch v := da.(type) {
				case float64:
					entry.DeliveryAttempt = int(v)
				case int:
					entry.DeliveryAttempt = v
				case int64:
					entry.DeliveryAttempt = int(v)
				}
			}
		}

		logs = append(logs, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return logs, nil
}

// getStringFromMap safely extracts a string value from a map
func getStringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// getTimestampFromEvent extracts timestamp from event map and returns as time.Time
// Returns zero time if timestamp cannot be parsed
func getTimestampFromEvent(event map[string]interface{}) time.Time {
	// Try different timestamp field names
	timestampFields := []string{"timestamp", "forwarded_at", "failed_at"}

	for _, field := range timestampFields {
		if ts, ok := event[field]; ok {
			if tsStr, ok := ts.(string); ok {
				// Try different timestamp formats
				formats := []string{
					time.RFC3339,                    // 2006-01-02T15:04:05Z07:00
					time.RFC3339Nano,                // 2006-01-02T15:04:05.999999999Z07:00
					"2006-01-02T15:04:05.000Z07:00", // Custom format with milliseconds
					"2006-01-02T15:04:05Z",          // UTC without timezone offset
				}

				for _, format := range formats {
					if t, err := time.Parse(format, tsStr); err == nil {
						return t
					}
				}
			}
		}
	}

	// Return zero time if cannot parse
	return time.Time{}
}

// sanitizeDomain sanitizes domain name for use in filesystem paths
func sanitizeDomain(domain string) string {
	safe := strings.ReplaceAll(domain, ".", "_")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, ":", "_")
	safe = strings.ReplaceAll(safe, "*", "_")
	safe = strings.ReplaceAll(safe, "?", "_")
	safe = strings.ReplaceAll(safe, "\"", "_")
	safe = strings.ReplaceAll(safe, "<", "_")
	safe = strings.ReplaceAll(safe, ">", "_")
	safe = strings.ReplaceAll(safe, "|", "_")
	return safe
}

// Start starts the HTTP server
func (s *Server) Start() error {
	logger.Logger.Info("Starting HTTP server", zap.String("addr", s.httpServer.Addr))
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server
func (s *Server) Shutdown(ctx context.Context) error {
	logger.Logger.Info("Shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}
