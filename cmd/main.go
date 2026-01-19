package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"calleventhub/internal/config"
	"calleventhub/internal/consumer"
	"calleventhub/internal/forwarder"
	"calleventhub/internal/http"
	"calleventhub/internal/logger"
	"calleventhub/internal/nats"
	"calleventhub/internal/store"

	"go.uber.org/zap"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFile := flag.String("log-file", "", "Path to log file (empty = stdout only, ignored if domain-logging is enabled)")
	domainLogging := flag.Bool("domain-logging", true, "Enable domain-based logging (logs grouped by domain in logs/ directory)")
	flag.Parse()

	// Initialize logger
	if err := logger.Init(*logLevel, *logFile, *domainLogging); err != nil {
		panic(err)
	}
	defer logger.Sync()

	logger.Logger.Info("Starting event-hub service")

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	// Create NATS publisher
	publisher, err := nats.NewPublisher(
		cfg.NATS.URL,
		cfg.NATS.StreamName,
		cfg.NATS.SubjectPattern,
	)
	if err != nil {
		logger.Logger.Fatal("Failed to create NATS publisher", zap.Error(err))
	}
	defer publisher.Close()

	// Create NATS consumer
	natsConsumer, err := nats.NewConsumer(
		cfg.NATS.URL,
		cfg.NATS.StreamName,
		cfg.NATS.SubjectPattern,
		"event-hub-consumer",
		cfg.NATS.AckWait,
		cfg.NATS.MaxDeliveries,
	)
	if err != nil {
		logger.Logger.Fatal("Failed to create NATS consumer", zap.Error(err))
	}
	defer natsConsumer.Close()

	// Create event store (keep last 10000 events)
	eventStore := store.NewStore(10000)

	// Create forwarder
	fwd := forwarder.NewForwarder(cfg, eventStore)

	// Create consumer service
	consumerService := consumer.NewConsumerService(cfg, natsConsumer, fwd)

	// Create HTTP handler
	httpHandler := http.NewHandler(publisher, eventStore, cfg, fwd, *configPath)

	// Create HTTP server
	httpServer := http.NewServer(cfg.Server.Port, httpHandler)

	// Start consumer service in background
	consumerErrChan := make(chan error, 1)
	go func() {
		if err := consumerService.Start(); err != nil {
			consumerErrChan <- err
		}
	}()

	// Start HTTP server in background
	httpErrChan := make(chan error, 1)
	go func() {
		if err := httpServer.Start(); err != nil {
			httpErrChan <- err
		}
	}()

	// Start config file watcher in background
	go watchConfigFile(*configPath, fwd, httpHandler)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Logger.Info("Service started successfully")

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		logger.Logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
	case err := <-httpErrChan:
		logger.Logger.Error("HTTP server error", zap.Error(err))
	case err := <-consumerErrChan:
		logger.Logger.Error("Consumer service error", zap.Error(err))
	}

	// Graceful shutdown
	logger.Logger.Info("Initiating graceful shutdown")

	// Stop accepting new HTTP requests
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop consumer service (this will stop processing new messages)
	consumerService.Stop()

	// Drain NATS subscription (wait for in-flight messages to complete)
	// The consumer will stop receiving new messages after Stop() is called
	// Give it a moment to finish processing current messages
	time.Sleep(2 * time.Second)

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Logger.Error("Error during HTTP server shutdown", zap.Error(err))
	}

	logger.Logger.Info("Shutdown complete")
}

// watchConfigFile watches the config file for changes and automatically reloads
func watchConfigFile(configPath string, fwd *forwarder.Forwarder, handler *http.Handler) {
	// Get initial file modification time
	initialStat, err := os.Stat(configPath)
	if err != nil {
		logger.Logger.Warn("Failed to stat config file for watching", zap.String("path", configPath), zap.Error(err))
		return
	}
	lastModTime := initialStat.ModTime()

	// Check file every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stat, err := os.Stat(configPath)
		if err != nil {
			logger.Logger.Warn("Failed to stat config file", zap.String("path", configPath), zap.Error(err))
			continue
		}

		// Check if file was modified
		if stat.ModTime().After(lastModTime) {
			lastModTime = stat.ModTime()
			logger.Logger.Info("Config file changed, reloading...", zap.String("path", configPath))

			// Reload config
			if err := fwd.ReloadConfig(configPath); err != nil {
				logger.Logger.Error("Failed to auto-reload config", zap.String("path", configPath), zap.Error(err))
				continue
			}

			// Update handler's config reference
			// Note: We need to access handler's internal fields, so we'll use a method
			handler.UpdateConfig(fwd.GetConfig())

			logger.Logger.Info("Config auto-reloaded successfully",
				zap.String("path", configPath),
				zap.Int("route_count", len(fwd.GetConfig().Routes)),
			)
		}
	}
}
