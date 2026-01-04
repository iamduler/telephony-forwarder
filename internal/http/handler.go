package http

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"calleventhub/internal/logger"
	"calleventhub/internal/nats"
	"calleventhub/internal/store"

	"go.uber.org/zap"
)

//go:embed web/dashboard.html
var dashboardHTML embed.FS

// Event represents the incoming event payload
// This matches the actual telephony signaling event structure
type Event struct {
	ActualHotline         string `json:"actual_hotline"`
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
	publisher *nats.Publisher
	store    *store.Store
}

// NewHandler creates a new HTTP handler
func NewHandler(publisher *nats.Publisher, eventStore *store.Store) *Handler {
	return &Handler{
		publisher: publisher,
		store:    eventStore,
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
	// Domain is required for routing
	if event.Domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	// Publish to NATS JetStream
	eventJSON, err := json.Marshal(event)
	if err != nil {
		logger.Logger.Error("Failed to marshal event", zap.Error(err), zap.String("call_id", event.CallID), zap.String("domain", event.Domain))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.publisher.Publish(eventJSON); err != nil {
		logger.Logger.Error("Failed to publish event", zap.Error(err), zap.String("call_id", event.CallID), zap.String("domain", event.Domain))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Logger.Info("Event received and published",
		zap.String("call_id", event.CallID),
		zap.String("domain", event.Domain),
		zap.String("direction", event.Direction),
		zap.String("state", event.State),
		zap.String("status", event.Status),
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

	// Get stats
	stats := h.store.GetStats()

	response := map[string]interface{}{
		"events_by_domain":     eventsByDomain,
		"failed_events_by_domain": failedEventsByDomain,
		"stats":                stats,
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
	
	// Serve dashboard
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
	htmlFS, err := fs.Sub(dashboardHTML, "web")
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

