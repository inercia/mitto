package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// GlobalEventsClient represents a connected client listening for global events.
type GlobalEventsClient struct {
	server   *Server
	conn     *websocket.Conn
	send     chan []byte
	done     chan struct{}
	clientIP string // For connection tracking cleanup
}

// GlobalEventsManager manages clients subscribed to global events.
type GlobalEventsManager struct {
	mu      sync.RWMutex
	clients map[*GlobalEventsClient]struct{}
}

// NewGlobalEventsManager creates a new global events manager.
func NewGlobalEventsManager() *GlobalEventsManager {
	return &GlobalEventsManager{
		clients: make(map[*GlobalEventsClient]struct{}),
	}
}

// Register adds a client to the manager.
func (m *GlobalEventsManager) Register(client *GlobalEventsClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[client] = struct{}{}
}

// Unregister removes a client from the manager.
func (m *GlobalEventsManager) Unregister(client *GlobalEventsClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, client)
}

// Broadcast sends a message to all connected clients.
func (m *GlobalEventsManager) Broadcast(msgType string, data interface{}) {
	msg := WSMessage{Type: msgType}
	if data != nil {
		msg.Data, _ = json.Marshal(data)
	}
	msgBytes, _ := json.Marshal(msg)

	m.mu.RLock()
	defer m.mu.RUnlock()

	for client := range m.clients {
		select {
		case client.send <- msgBytes:
		default:
			// Client too slow, skip
		}
	}
}

// ClientCount returns the number of connected clients.
func (m *GlobalEventsManager) ClientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// handleGlobalEventsWS handles WebSocket connections for global events.
func (s *Server) handleGlobalEventsWS(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIPWithProxyCheck(r)

	// Check connection limit per IP
	if s.connectionTracker != nil && !s.connectionTracker.TryAdd(clientIP) {
		if s.logger != nil {
			s.logger.Warn("Global events WebSocket rejected: too many connections",
				"client_ip", clientIP)
		}
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	}

	// Use secure upgrader
	secureUpgrader := s.getSecureUpgrader()
	conn, err := secureUpgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.connectionTracker != nil {
			s.connectionTracker.Remove(clientIP)
		}
		if s.logger != nil {
			s.logger.Error("Global events WebSocket upgrade failed", "error", err)
		}
		return
	}

	// Apply security settings
	configureWebSocketConn(conn, s.wsSecurityConfig)

	client := &GlobalEventsClient{
		server:   s,
		conn:     conn,
		send:     make(chan []byte, 64),
		done:     make(chan struct{}),
		clientIP: clientIP,
	}

	s.eventsManager.Register(client)

	go client.writePump()
	go client.readPump()

	// Send initial connected message
	client.sendMessage(WSMsgTypeConnected, map[string]string{
		"acp_server": s.config.ACPServer,
	})
}

func (c *GlobalEventsClient) readPump() {
	defer func() {
		c.server.eventsManager.Unregister(c)
		// Release connection slot
		if c.server.connectionTracker != nil && c.clientIP != "" {
			c.server.connectionTracker.Remove(c.clientIP)
		}
		close(c.done)
		c.conn.Close()
	}()

	// We don't expect any messages from clients on this WebSocket,
	// but we need to read to detect disconnection
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

func (c *GlobalEventsClient) writePump() {
	// Create ping ticker using server's WebSocket security config
	pingPeriod := c.server.wsSecurityConfig.PingPeriod
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(c.server.wsSecurityConfig.WriteWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(c.server.wsSecurityConfig.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *GlobalEventsClient) sendMessage(msgType string, data interface{}) {
	msg := WSMessage{Type: msgType}
	if data != nil {
		msg.Data, _ = json.Marshal(data)
	}
	msgBytes, _ := json.Marshal(msg)

	select {
	case c.send <- msgBytes:
	default:
	}
}
