package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
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
// This is non-blocking - if the send buffer is full, the message is dropped.
func (w *WSConn) SendMessage(msgType string, data interface{}) {
	var dataJSON json.RawMessage
	if data != nil {
		dataJSON, _ = json.Marshal(data)
	}
	msg := WSMessage{Type: msgType, Data: dataJSON}
	msgBytes, _ := json.Marshal(msg)

	select {
	case w.send <- msgBytes:
	default:
		// Send buffer full, drop message
		if w.logger != nil {
			w.logger.Warn("WebSocket send buffer full, dropping message",
				"type", msgType, "client_ip", w.clientIP)
		}
	}
}

// SendError sends an error message to the client.
func (w *WSConn) SendError(message string) {
	w.SendMessage(WSMsgTypeError, map[string]string{"message": message})
}

// SendRaw sends raw bytes to the client.
// This is non-blocking - if the send buffer is full, the message is dropped.
func (w *WSConn) SendRaw(data []byte) {
	select {
	case w.send <- data:
	default:
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
				w.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w.conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			w.conn.SetWriteDeadline(time.Now().Add(w.config.WriteWait))
			if err := w.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-ctx.Done():
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
