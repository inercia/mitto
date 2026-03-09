package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// sendBackpressureTimeout is the maximum time to wait when the send buffer is full
	// before closing the connection. This absorbs short bursts while ensuring slow
	// clients are disconnected rather than having their messages silently dropped.
	// A disconnected client will reconnect and catch up from persisted events.
	sendBackpressureTimeout = 100 * time.Millisecond
)

// WSConn wraps a WebSocket connection with common functionality for
// message sending, ping/pong keepalive, and connection lifecycle management.
// It provides a unified interface used by both SessionWSClient and GlobalEventsClient.
type WSConn struct {
	conn     *websocket.Conn
	send     chan []byte
	config   WebSocketSecurityConfig
	logger   *slog.Logger
	clientIP string

	// Connection tracking for cleanup
	tracker *ConnectionTracker
}

// WSConnConfig contains configuration for creating a new WSConn.
type WSConnConfig struct {
	Conn     *websocket.Conn
	Config   WebSocketSecurityConfig
	Logger   *slog.Logger
	ClientIP string
	Tracker  *ConnectionTracker
	SendSize int // Size of send channel buffer (default: 256)
}

// NewWSConn creates a new WebSocket connection wrapper.
func NewWSConn(cfg WSConnConfig) *WSConn {
	sendSize := cfg.SendSize
	if sendSize <= 0 {
		sendSize = 256
	}

	// Configure the connection with security settings
	configureWebSocketConn(cfg.Conn, cfg.Config)

	return &WSConn{
		conn:     cfg.Conn,
		send:     make(chan []byte, sendSize),
		config:   cfg.Config,
		logger:   cfg.Logger,
		clientIP: cfg.ClientIP,
		tracker:  cfg.Tracker,
	}
}

// SendMessage sends a typed message with optional data payload.
// Uses graceful backpressure: if the send buffer is full, waits briefly for
// space. If the buffer remains full after the timeout, the connection is closed
// to force the slow client to reconnect and catch up from persisted events.
// This prevents silent message drops that cause sequence gaps on the client.
func (w *WSConn) SendMessage(msgType string, data interface{}) {
	var dataJSON json.RawMessage
	if data != nil {
		dataJSON, _ = json.Marshal(data)
	}
	msg := WSMessage{Type: msgType, Data: dataJSON}
	msgBytes, _ := json.Marshal(msg)

	w.sendWithBackpressure(msgBytes, msgType)
}

// SendError sends an error message to the client.
func (w *WSConn) SendError(message string) {
	w.SendMessage(WSMsgTypeError, map[string]string{"message": message})
}

// SendRaw sends raw bytes to the client.
// Uses graceful backpressure: waits briefly if buffer is full, then closes
// the connection if the client can't keep up.
func (w *WSConn) SendRaw(data []byte) {
	w.sendWithBackpressure(data, "raw")
}

// sendWithBackpressure attempts to enqueue data on the send channel.
// First tries a non-blocking send for the fast path. If the buffer is full,
// waits up to sendBackpressureTimeout for space. If the timeout expires,
// closes the WebSocket connection so the slow client is forced to reconnect
// and sync from persisted events (rather than silently dropping messages
// which causes unrecoverable sequence gaps).
func (w *WSConn) sendWithBackpressure(data []byte, msgType string) {
	// Fast path: non-blocking send
	select {
	case w.send <- data:
		return
	default:
	}

	// Slow path: buffer is full, apply backpressure with timeout
	if w.logger != nil {
		w.logger.Warn("WebSocket send buffer full, applying backpressure",
			"type", msgType, "client_ip", w.clientIP,
			"buffer_cap", cap(w.send), "timeout", sendBackpressureTimeout)
	}

	timer := time.NewTimer(sendBackpressureTimeout)
	defer timer.Stop()

	select {
	case w.send <- data:
		// Buffer drained enough, message sent
		if w.logger != nil {
			w.logger.Debug("WebSocket backpressure resolved",
				"type", msgType, "client_ip", w.clientIP)
		}
	case <-timer.C:
		// Client is too slow — close the connection to force reconnection.
		// The client will reconnect and sync from persisted events via load_events.
		if w.logger != nil {
			w.logger.Warn("WebSocket client too slow, closing connection for reconnect",
				"type", msgType, "client_ip", w.clientIP,
				"buffer_cap", cap(w.send))
		}
		w.conn.Close()
	}
}

// Close closes the underlying WebSocket connection.
func (w *WSConn) Close() error {
	return w.conn.Close()
}

// ReleaseConnectionSlot releases the connection slot from the tracker.
// Should be called during cleanup.
func (w *WSConn) ReleaseConnectionSlot() {
	if w.tracker != nil && w.clientIP != "" {
		w.tracker.Remove(w.clientIP)
	}
}

// WritePump pumps messages from the send channel to the WebSocket connection.
// It also handles ping/pong keepalive. This should be run in a goroutine.
// The done channel is closed when the pump exits.
// The ctx is used to signal shutdown.
func (w *WSConn) WritePump(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(w.config.PingPeriod)
	defer func() {
		ticker.Stop()
		w.conn.Close()
		if done != nil {
			close(done)
		}
	}()

	for {
		select {
		case message, ok := <-w.send:
			w.conn.SetWriteDeadline(time.Now().Add(w.config.WriteWait))
			if !ok {
				w.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			w.conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			w.conn.SetWriteDeadline(time.Now().Add(w.config.WriteWait))
			if err := w.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				// Best-effort close frame before exit — the write may fail
				// if the connection is already degraded, which is fine.
				w.conn.SetWriteDeadline(time.Now().Add(time.Second))
				w.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "ping failed"))
				return
			}
		case <-ctx.Done():
			// Best-effort close frame on context cancellation (e.g., readPump exited).
			// Without this, the client sees code 1006 (abnormal closure) instead of
			// a proper close code, which complicates client-side reconnection logic.
			w.conn.SetWriteDeadline(time.Now().Add(time.Second))
			w.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"))
			return
		}
	}
}

// ReadMessage reads a single message from the WebSocket connection.
// Returns the message bytes and any error.
func (w *WSConn) ReadMessage() ([]byte, error) {
	_, message, err := w.conn.ReadMessage()
	return message, err
}
