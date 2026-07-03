package web

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// The queue REST handlers and their tests now live in internal/web/handlers.
// What remains here covers the queue helpers that stay in the web package:
// the QueueConfigResponse constructor (used by the WebSocket connect message)
// and the notifyQueue* broadcast helpers (also used by session_api.go).

func TestNewQueueConfigResponse(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.QueueConfig
		expected QueueConfigResponse
	}{
		{
			name:   "nil config uses defaults",
			config: nil,
			expected: QueueConfigResponse{
				Enabled:      true,                       // default
				MaxSize:      config.DefaultQueueMaxSize, // 10
				DelaySeconds: 0,                          // default
			},
		},
		{
			name: "custom config",
			config: &config.QueueConfig{
				Enabled:      boolPtr(false),
				MaxSize:      intPtr(50),
				DelaySeconds: 5,
			},
			expected: QueueConfigResponse{
				Enabled:      false,
				MaxSize:      50,
				DelaySeconds: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewQueueConfigResponse(tt.config)
			if got.Enabled != tt.expected.Enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.expected.Enabled)
			}
			if got.MaxSize != tt.expected.MaxSize {
				t.Errorf("MaxSize = %v, want %v", got.MaxSize, tt.expected.MaxSize)
			}
			if got.DelaySeconds != tt.expected.DelaySeconds {
				t.Errorf("DelaySeconds = %v, want %v", got.DelaySeconds, tt.expected.DelaySeconds)
			}
		})
	}
}

func TestNotifyQueueUpdate_NilSessionManager(t *testing.T) {
	server := &Server{
		sessionManager: nil,
	}

	// Should not panic
	server.notifyQueueUpdate("session-id", "add", "msg-id")
}

func TestNotifyQueueReorder_NilSessionManager(t *testing.T) {
	server := &Server{
		sessionManager: nil,
	}

	// Should not panic
	server.notifyQueueReorder("session-id", nil)
}

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}
