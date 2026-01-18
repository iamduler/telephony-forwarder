package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger is a global logger instance
var Logger *zap.Logger

// DomainLoggerManager manages loggers per domain
type DomainLoggerManager struct {
	baseDir       string
	level         zapcore.Level
	encoder       zapcore.Encoder
	loggers       map[string]*zap.Logger // key: domain-date (e.g., "domain.com-2026-01-04")
	mu            sync.RWMutex
	cleanupTicker *time.Ticker
	stopCleanup   chan bool
}

var domainLoggerManager *DomainLoggerManager
var domainLoggerOnce sync.Once

// localTimeEncoder encodes time in local timezone with ISO8601 format
func localTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	// Convert to local timezone
	localTime := t.Local()
	// Format as ISO8601 with timezone offset
	enc.AppendString(localTime.Format("2006-01-02T15:04:05.000Z07:00"))
}

// Init initializes the global logger
// logFile: path to log file (empty string = stdout only)
// enableDomainLogging: if true, logs will be grouped by domain in logs/ directory
func Init(level string, logFile string, enableDomainLogging bool) error {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zapLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	// Use local timezone instead of UTC
	config.EncoderConfig.EncodeTime = localTimeEncoder

	// Build encoder
	encoder := zapcore.NewJSONEncoder(config.EncoderConfig)

	// Create cores for output
	var cores []zapcore.Core

	// Always log to stdout/stderr
	stdoutCore := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		zap.NewAtomicLevelAt(zapLevel),
	)
	cores = append(cores, stdoutCore)

	// If log file is specified, also log to file
	if logFile != "" && !enableDomainLogging {
		// Ensure log directory exists
		logDir := filepath.Dir(logFile)
		if logDir != "." && logDir != "" {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				return err
			}
		}

		// Use lumberjack for log rotation
		fileWriter := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    100,  // megabytes
			MaxBackups: 5,    // keep 5 backup files
			MaxAge:     30,   // days
			Compress:   true, // compress old log files
		}

		fileCore := zapcore.NewCore(
			encoder,
			zapcore.AddSync(fileWriter),
			zap.NewAtomicLevelAt(zapLevel),
		)
		cores = append(cores, fileCore)
	}

	// Initialize domain logger manager if enabled
	if enableDomainLogging {
		domainLoggerOnce.Do(func() {
			baseDir := "logs"
			if logFile != "" {
				// Use directory from logFile if provided
				baseDir = filepath.Dir(logFile)
				if baseDir == "." || baseDir == "" {
					baseDir = "logs"
				}
			}

			domainLoggerManager = &DomainLoggerManager{
				baseDir:     baseDir,
				level:       zapLevel,
				encoder:     encoder,
				loggers:     make(map[string]*zap.Logger),
				stopCleanup: make(chan bool),
			}

			// Start cleanup routine to remove old logger references
			go domainLoggerManager.cleanupRoutine()
		})
	}

	// Combine cores
	core := zapcore.NewTee(cores...)

	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	Logger = logger
	return nil
}

// getDomainLogger returns a logger for a specific domain and date
func (dlm *DomainLoggerManager) getDomainLogger(domain, date string) *zap.Logger {
	key := fmt.Sprintf("%s-%s", domain, date)

	dlm.mu.RLock()
	if logger, exists := dlm.loggers[key]; exists {
		dlm.mu.RUnlock()
		return logger
	}
	dlm.mu.RUnlock()

	// Create new logger for this domain-date combination
	dlm.mu.Lock()
	defer dlm.mu.Unlock()

	// Double check after acquiring write lock
	if logger, exists := dlm.loggers[key]; exists {
		return logger
	}

	// Sanitize domain name for filesystem
	safeDomain := sanitizeDomain(domain)
	domainDir := filepath.Join(dlm.baseDir, safeDomain)

	// Ensure domain directory exists
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		// Fallback to base logger if directory creation fails
		return Logger
	}

	// Create log file path: logs/domain/YYYY-MM-DD.log
	logFile := filepath.Join(domainDir, fmt.Sprintf("%s.log", date))

	// Use lumberjack for log rotation (though we rotate by date)
	fileWriter := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    500,  // megabytes (large enough for daily logs)
		MaxBackups: 30,   // keep 30 days of logs
		MaxAge:     30,   // days
		Compress:   true, // compress old log files
	}

	fileCore := zapcore.NewCore(
		dlm.encoder,
		zapcore.AddSync(fileWriter),
		zap.NewAtomicLevelAt(dlm.level),
	)

	// Combine with stdout
	core := zapcore.NewTee(
		zapcore.NewCore(
			dlm.encoder,
			zapcore.AddSync(os.Stdout),
			zap.NewAtomicLevelAt(dlm.level),
		),
		fileCore,
	)

	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	dlm.loggers[key] = logger

	return logger
}

// sanitizeDomain sanitizes domain name for use in filesystem paths
func sanitizeDomain(domain string) string {
	// Replace invalid filesystem characters
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

// cleanupRoutine periodically cleans up old logger references
func (dlm *DomainLoggerManager) cleanupRoutine() {
	dlm.cleanupTicker = time.NewTicker(1 * time.Hour)
	defer dlm.cleanupTicker.Stop()

	for {
		select {
		case <-dlm.cleanupTicker.C:
			dlm.mu.Lock()
			// Keep only today's and yesterday's loggers in memory
			today := time.Now().Format("2006-01-02")
			yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

			for key := range dlm.loggers {
				if !strings.HasSuffix(key, "-"+today) && !strings.HasSuffix(key, "-"+yesterday) {
					delete(dlm.loggers, key)
				}
			}
			dlm.mu.Unlock()
		case <-dlm.stopCleanup:
			return
		}
	}
}

// LogWithDomain logs a message and routes it to domain-specific log file
func LogWithDomain(level zapcore.Level, msg string, fields ...zap.Field) {
	if domainLoggerManager == nil {
		// Fallback to global logger
		switch level {
		case zapcore.DebugLevel:
			Logger.Debug(msg, fields...)
		case zapcore.InfoLevel:
			Logger.Info(msg, fields...)
		case zapcore.WarnLevel:
			Logger.Warn(msg, fields...)
		case zapcore.ErrorLevel:
			Logger.Error(msg, fields...)
		}
		return
	}

	// Extract domain from fields
	var domain string
	otherFields := make([]zap.Field, 0, len(fields))

	for _, field := range fields {
		if field.Key == "domain" {
			// Extract domain value
			if field.Type == zapcore.StringType {
				domain = field.String
			} else {
				domain = fmt.Sprintf("%v", field.Interface)
			}
			otherFields = append(otherFields, field)
		} else {
			otherFields = append(otherFields, field)
		}
	}

	// If domain is found, route to domain-specific logger
	if domain != "" {
		date := time.Now().Format("2006-01-02")
		domainLogger := domainLoggerManager.getDomainLogger(domain, date)
		switch level {
		case zapcore.DebugLevel:
			domainLogger.Debug(msg, otherFields...)
		case zapcore.InfoLevel:
			domainLogger.Info(msg, otherFields...)
		case zapcore.WarnLevel:
			domainLogger.Warn(msg, otherFields...)
		case zapcore.ErrorLevel:
			domainLogger.Error(msg, otherFields...)
		}
	} else {
		// No domain, use global logger
		switch level {
		case zapcore.DebugLevel:
			Logger.Debug(msg, fields...)
		case zapcore.InfoLevel:
			Logger.Info(msg, fields...)
		case zapcore.WarnLevel:
			Logger.Warn(msg, fields...)
		case zapcore.ErrorLevel:
			Logger.Error(msg, fields...)
		}
	}
}

// Sync flushes any buffered log entries
func Sync() {
	if Logger != nil {
		_ = Logger.Sync()
	}
	if domainLoggerManager != nil {
		domainLoggerManager.mu.RLock()
		for _, logger := range domainLoggerManager.loggers {
			_ = logger.Sync()
		}
		domainLoggerManager.mu.RUnlock()
	}
}
