// Package logging provides centralized logging configuration for Mitto.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

var (
	// globalLogger is the application-wide logger
	globalLogger *slog.Logger
	globalMu     sync.RWMutex

	// logFile holds the open log file (if any) for cleanup
	logFile   *os.File
	logFileMu sync.Mutex

	// allowedComponents stores the set of components to log (empty means all)
	allowedComponents map[string]bool
	componentsMu      sync.RWMutex
)

// Config holds logging configuration.
type Config struct {
	// Level is the minimum log level (debug, info, warn, error)
	Level string
	// LogFile is an optional file path to write logs to (in addition to console)
	LogFile string
	// JSON enables JSON output format
	JSON bool
	// Components is a list of component names to include in logs (empty means all)
	Components []string
}

// Initialize sets up the global logger with the given configuration.
// If logFile is specified, logs are written to both console and file.
func Initialize(cfg Config) error {
	level := parseLevel(cfg.Level)

	// Store allowed components
	componentsMu.Lock()
	if len(cfg.Components) > 0 {
		allowedComponents = make(map[string]bool)
		for _, c := range cfg.Components {
			allowedComponents[c] = true
		}
	} else {
		allowedComponents = nil // nil means all components allowed
	}
	componentsMu.Unlock()

	var writers []io.Writer
	writers = append(writers, os.Stderr)

	// Open log file if specified
	if cfg.LogFile != "" {
		logFileMu.Lock()
		defer logFileMu.Unlock()

		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", cfg.LogFile, err)
		}
		logFile = f
		writers = append(writers, f)
	}

	// Create multi-writer
	w := io.MultiWriter(writers...)

	// Create handler based on format
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: level,
	}

	if cfg.JSON {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	logger := slog.New(handler)

	globalMu.Lock()
	globalLogger = logger
	globalMu.Unlock()

	// Also set as default slog logger
	slog.SetDefault(logger)

	return nil
}

// Get returns the global logger.
// If Initialize hasn't been called, returns slog.Default().
func Get() *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()

	if globalLogger == nil {
		return slog.Default()
	}
	return globalLogger
}

// Close cleans up logging resources (closes log file if open).
func Close() error {
	logFileMu.Lock()
	defer logFileMu.Unlock()

	if logFile != nil {
		err := logFile.Close()
		logFile = nil
		return err
	}
	return nil
}

// parseLevel converts a string level to slog.Level.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// isComponentAllowed checks if a component should be logged.
func isComponentAllowed(component string) bool {
	componentsMu.RLock()
	defer componentsMu.RUnlock()

	// If no components specified, allow all
	if allowedComponents == nil {
		return true
	}
	return allowedComponents[component]
}

// componentFilterHandler wraps a slog.Handler and filters based on component.
type componentFilterHandler struct {
	inner     slog.Handler
	component string
}

func (h *componentFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if !isComponentAllowed(h.component) {
		return false
	}
	return h.inner.Enabled(ctx, level)
}

func (h *componentFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	if !isComponentAllowed(h.component) {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

func (h *componentFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &componentFilterHandler{
		inner:     h.inner.WithAttrs(attrs),
		component: h.component,
	}
}

func (h *componentFilterHandler) WithGroup(name string) slog.Handler {
	return &componentFilterHandler{
		inner:     h.inner.WithGroup(name),
		component: h.component,
	}
}

// WithComponent returns a logger with a component attribute.
// If component filtering is enabled and this component is not in the allowed list,
// the returned logger will be a no-op logger.
func WithComponent(component string) *slog.Logger {
	base := Get()
	handler := &componentFilterHandler{
		inner:     base.Handler().WithAttrs([]slog.Attr{slog.String("component", component)}),
		component: component,
	}
	return slog.New(handler)
}

// Web returns a logger for web-related events.
func Web() *slog.Logger {
	return WithComponent("web")
}

// Auth returns a logger for authentication events.
func Auth() *slog.Logger {
	return WithComponent("auth")
}

// Hook returns a logger for hook events.
func Hook() *slog.Logger {
	return WithComponent("hook")
}

// Session returns a logger for session events.
func Session() *slog.Logger {
	return WithComponent("session")
}

// Shutdown returns a logger for shutdown events.
func Shutdown() *slog.Logger {
	return WithComponent("shutdown")
}

// WithSessionContext returns a logger with full session context.
// This creates a child logger that automatically includes session_id, working_dir,
// and acp_server in all log messages.
func WithSessionContext(base *slog.Logger, sessionID, workingDir, acpServer string) *slog.Logger {
	if base == nil {
		return nil
	}
	return base.With(
		"session_id", sessionID,
		"working_dir", workingDir,
		"acp_server", acpServer,
	)
}

// WithClient returns a logger with WebSocket client context.
// This creates a child logger that includes client_id and session_id.
func WithClient(base *slog.Logger, clientID, sessionID string) *slog.Logger {
	if base == nil {
		return nil
	}
	return base.With(
		"client_id", clientID,
		"session_id", sessionID,
	)
}
