// Package logging provides centralized logging configuration for Mitto.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// globalLogger is the application-wide logger
	globalLogger *slog.Logger
	globalMu     sync.RWMutex

	// logWriter holds the log file writer (if any) for cleanup
	// Can be *os.File or *lumberjack.Logger
	logWriter   io.WriteCloser
	logWriterMu sync.Mutex

	// allowedComponents stores the set of components to log (empty means all)
	allowedComponents map[string]bool
	componentsMu      sync.RWMutex
)

// FileLogConfig holds configuration for file-based logging with rotation.
type FileLogConfig struct {
	// Path is the file path for the log file.
	// Empty string disables file logging.
	Path string

	// MaxSizeMB is the maximum size of the log file in megabytes before rotation.
	// Default: 10MB
	MaxSizeMB int

	// MaxBackups is the maximum number of old log files to retain.
	// Default: 3
	MaxBackups int

	// Compress determines if rotated log files should be compressed.
	// Default: false
	Compress bool
}

// DefaultFileLogConfig returns the default file log configuration.
func DefaultFileLogConfig() FileLogConfig {
	return FileLogConfig{
		MaxSizeMB:  10,
		MaxBackups: 3,
		Compress:   false,
	}
}

// Config holds logging configuration.
type Config struct {
	// Level is the minimum log level for console output (debug, info, warn, error)
	Level string
	// FileLevel is the minimum log level for file output (debug, info, warn, error).
	// If empty, defaults to Level.
	FileLevel string
	// LogFile is an optional file path to write logs to (in addition to console)
	// Deprecated: Use FileLog for rotation support
	LogFile string
	// FileLog is the configuration for file-based logging with rotation.
	// Takes precedence over LogFile if both are specified.
	FileLog *FileLogConfig
	// JSON enables JSON output format
	JSON bool
	// Components is a list of component names to include in logs (empty means all)
	Components []string
}

// Initialize sets up the global logger with the given configuration.
// If FileLog or LogFile is specified, logs are written to both console and file.
// FileLog takes precedence and supports log rotation via lumberjack.
// If FileLevel differs from Level, separate handlers with different log levels are used.
func Initialize(cfg Config) error {
	consoleLevel := parseLevel(cfg.Level)
	fileLevel := consoleLevel
	if cfg.FileLevel != "" {
		fileLevel = parseLevel(cfg.FileLevel)
	}

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

	logWriterMu.Lock()
	defer logWriterMu.Unlock()

	// Determine file writer (if any)
	var fileWriter io.Writer
	if cfg.FileLog != nil && cfg.FileLog.Path != "" {
		// Apply defaults
		maxSize := cfg.FileLog.MaxSizeMB
		if maxSize <= 0 {
			maxSize = 10
		}
		maxBackups := cfg.FileLog.MaxBackups
		if maxBackups < 0 {
			maxBackups = 3
		}

		// Create lumberjack logger for rotation
		lj := &lumberjack.Logger{
			Filename:   cfg.FileLog.Path,
			MaxSize:    maxSize,    // megabytes
			MaxBackups: maxBackups, // number of backups
			MaxAge:     0,          // don't delete old files based on age
			Compress:   cfg.FileLog.Compress,
		}
		logWriter = lj
		fileWriter = lj
	} else if cfg.LogFile != "" {
		// Legacy: simple file logging without rotation
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", cfg.LogFile, err)
		}
		logWriter = f
		fileWriter = f
	}

	// Create handler(s) based on configuration
	var handler slog.Handler

	// Helper to create a handler for a writer with a given level
	createHandler := func(w io.Writer, level slog.Level) slog.Handler {
		opts := &slog.HandlerOptions{Level: level}
		if cfg.JSON {
			return slog.NewJSONHandler(w, opts)
		}
		return slog.NewTextHandler(w, opts)
	}

	if fileWriter != nil && fileLevel != consoleLevel {
		// Different levels: use multiHandler to fan out to both
		consoleHandler := createHandler(os.Stderr, consoleLevel)
		fileHandler := createHandler(fileWriter, fileLevel)
		handler = &multiHandler{handlers: []slog.Handler{consoleHandler, fileHandler}}
	} else if fileWriter != nil {
		// Same level: use MultiWriter for efficiency
		w := io.MultiWriter(os.Stderr, fileWriter)
		handler = createHandler(w, consoleLevel)
	} else {
		// Console only
		handler = createHandler(os.Stderr, consoleLevel)
	}

	logger := slog.New(handler)

	globalMu.Lock()
	globalLogger = logger
	globalMu.Unlock()

	// Also set as default slog logger
	slog.SetDefault(logger)

	return nil
}

// multiHandler fans out log records to multiple handlers.
// It is used when console and file have different log levels.
type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// Enabled if ANY handler is enabled at this level
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	// Send to all handlers that are enabled
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			if err := handler.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
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
	logWriterMu.Lock()
	defer logWriterMu.Unlock()

	if logWriter != nil {
		err := logWriter.Close()
		logWriter = nil
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

// MCP returns a logger for MCP server events.
func MCP() *slog.Logger {
	return WithComponent("mcp")
}

// DowngradeInfoToDebug returns a logger that downgrades INFO messages to DEBUG level.
// This is useful for third-party libraries (like acp-go-sdk) that log at INFO level
// for messages that should be DEBUG (e.g., "peer connection closed").
func DowngradeInfoToDebug(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}
	return slog.New(&downgradeHandler{inner: logger.Handler()})
}

// downgradeHandler wraps a slog.Handler and downgrades INFO to DEBUG.
type downgradeHandler struct {
	inner slog.Handler
}

func (h *downgradeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// If checking INFO, check if DEBUG is enabled (since we'll downgrade)
	if level == slog.LevelInfo {
		return h.inner.Enabled(ctx, slog.LevelDebug)
	}
	return h.inner.Enabled(ctx, level)
}

func (h *downgradeHandler) Handle(ctx context.Context, r slog.Record) error {
	// Downgrade INFO to DEBUG
	if r.Level == slog.LevelInfo {
		newRecord := slog.NewRecord(r.Time, slog.LevelDebug, r.Message, r.PC)
		// Copy attributes from original record
		r.Attrs(func(a slog.Attr) bool {
			newRecord.AddAttrs(a)
			return true
		})
		return h.inner.Handle(ctx, newRecord)
	}
	return h.inner.Handle(ctx, r)
}

func (h *downgradeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &downgradeHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *downgradeHandler) WithGroup(name string) slog.Handler {
	return &downgradeHandler{inner: h.inner.WithGroup(name)}
}
