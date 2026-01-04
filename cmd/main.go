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

	// Create event store (keep last 1000 events)
	eventStore := store.NewStore(1000)

	// Create forwarder
	fwd := forwarder.NewForwarder(cfg, eventStore)

	// Create consumer service
	consumerService := consumer.NewConsumerService(cfg, natsConsumer, fwd)

	// Create HTTP handler
	httpHandler := http.NewHandler(publisher, eventStore)

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
