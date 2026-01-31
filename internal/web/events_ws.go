package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

// GlobalEventsClient represents a connected client listening for global events.
type GlobalEventsClient struct {
	server *Server
	wsConn *WSConn
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
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
		client.wsConn.SendRaw(msgBytes)
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

	ctx, cancel := context.WithCancel(context.Background())

	// Create shared WebSocket connection wrapper
	wsConn := NewWSConn(WSConnConfig{
		Conn:     conn,
		Config:   s.wsSecurityConfig,
		Logger:   s.logger,
		ClientIP: clientIP,
		Tracker:  s.connectionTracker,
		SendSize: 64,
	})

	client := &GlobalEventsClient{
		server: s,
		wsConn: wsConn,
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	s.eventsManager.Register(client)

	go client.writePump()
	go client.readPump()

	// Send initial connected message
	client.wsConn.SendMessage(WSMsgTypeConnected, map[string]string{
		"acp_server": s.config.ACPServer,
	})
}

func (c *GlobalEventsClient) readPump() {
	defer func() {
		c.server.eventsManager.Unregister(c)
		c.cancel()
		c.wsConn.ReleaseConnectionSlot()
		c.wsConn.Close()
	}()

	// We don't expect any messages from clients on this WebSocket,
	// but we need to read to detect disconnection
	for {
		_, err := c.wsConn.ReadMessage()
		if err != nil {
			return
		}
	}
}

func (c *GlobalEventsClient) writePump() {
	// Use WSConn's WritePump - it handles ping/pong and message sending
	// Pass done channel so it closes when pump exits
	c.wsConn.WritePump(c.ctx, c.done)
}
