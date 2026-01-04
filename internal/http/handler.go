package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"calleventhub/internal/logger"
	"calleventhub/internal/nats"

	"go.uber.org/zap"
)

// Event represents the incoming event payload
type Event struct {
	EventID   string                 `json:"event_id"`
	Domain    string                 `json:"domain"`
	Type      string                 `json:"type"`
	Timestamp int64                  `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
}

// Handler handles HTTP requests
type Handler struct {
	publisher *nats.Publisher
}

// NewHandler creates a new HTTP handler
func NewHandler(publisher *nats.Publisher) *Handler {
	return &Handler{
		publisher: publisher,
	}
}

// HandleEvents handles POST /events
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		logger.Logger.Warn("Failed to decode event", zap.Error(err))
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if event.EventID == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}
	if event.Domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}
	if event.Type == "" {
		http.Error(w, "type is required", http.StatusBadRequest)
		return
	}

	// Publish to NATS JetStream
	eventJSON, err := json.Marshal(event)
	if err != nil {
		logger.Logger.Error("Failed to marshal event", zap.Error(err), zap.String("event_id", event.EventID))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.publisher.Publish(eventJSON); err != nil {
		logger.Logger.Error("Failed to publish event", zap.Error(err), zap.String("event_id", event.EventID))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Logger.Info("Event received and published",
		zap.String("event_id", event.EventID),
		zap.String("domain", event.Domain),
		zap.String("type", event.Type),
	)

	w.WriteHeader(http.StatusAccepted)
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

// Server wraps the HTTP server
type Server struct {
	httpServer *http.Server
	handler    *Handler
}

// NewServer creates a new HTTP server
func NewServer(port int, handler *Handler) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/events", handler.HandleEvents)
	mux.HandleFunc("/health", handler.HandleHealth)

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

