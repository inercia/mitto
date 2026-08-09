// Package client (import path github.com/inercia/mitto/pkg/api) provides a Go
// client for connecting to the Mitto backend.
//
// By default the client is unauthenticated, which is useful for integration
// testing and for CLI tools talking to a server with no auth configured. See
// # Authentication below for the shared-token and interactive login modes.
//
// # Basic Usage
//
// Create a client and list sessions:
//
//	c := client.New("http://localhost:8080")
//	sessions, err := c.ListSessions()
//
// Create a new session:
//
//	session, err := c.CreateSession(client.CreateSessionRequest{
//	    Name:       "my-session",
//	    WorkingDir: "/path/to/project",
//	})
//
// # WebSocket Session
//
// Connect to a session for real-time interaction:
//
//	ctx := context.Background()
//	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
//	    OnConnected: func(sessionID, clientID, acpServer string) {
//	        fmt.Printf("Connected to %s\n", sessionID)
//	    },
//	    OnAgentMessage: func(html string) {
//	        fmt.Printf("Agent: %s\n", html)
//	    },
//	    OnPromptComplete: func(eventCount int) {
//	        fmt.Printf("Done! %d events\n", eventCount)
//	    },
//	})
//	defer sess.Close()
//
//	// Send a message
//	sess.SendPrompt("Hello, world!")
//
// # Simplified Prompt Helper
//
// For simple request-response patterns, use PromptAndWait:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//
//	result, err := c.PromptAndWait(ctx, session.SessionID, "Explain this code")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	fmt.Printf("Got %d messages, %d tool calls\n",
//	    len(result.Messages), len(result.ToolCalls))
//
// # Authentication
//
// Three modes are supported, matching the backend's authentication options
// (internal/web/middleware/auth.go):
//
//   - None (default): zero-config, used by every existing test. Do nothing.
//
//   - Shared token: authenticate every REST request and the WebSocket
//     handshake with "Authorization: Bearer <token>", matching the
//     deployment-wide shared token the operator configures on the server.
//     Use WithBearerToken for a fixed token, or WithTokenSupplier to source
//     it lazily (environment variable, keychain, config file) and support
//     rotation without reconstructing the Client:
//
//     c := client.New(baseURL, client.WithTokenSupplier(func() (string, error) {
//     return os.Getenv("MITTO_TOKEN"), nil
//     }))
//
//   - Cookie login: for parity with the browser, call Login with a
//     username/password to obtain a session cookie plus CSRF token, used
//     automatically on subsequent REST requests and WebSocket connections:
//
//     c := client.New(baseURL)
//     if err := c.Login(ctx, "user", "pass"); err != nil { ... }
//     defer c.Logout(ctx)
//
// In every mode, the token/session credential is never logged and never
// placed in a URL or query string.
//
// # Thread Safety
//
// The Client and Session types are safe for concurrent use from multiple
// goroutines. However, the SessionCallbacks are invoked from a single
// goroutine (the WebSocket read loop), so callback implementations must
// be thread-safe if they access shared state.
//
// # Errors
//
// Non-2xx HTTP responses are returned as *APIError, which carries the
// parsed error envelope (Status, Code, Message, Details) plus the raw
// response Body for callers that need custom parsing. Both the canonical
// nested envelope and the legacy flat shape used by a few external-stable
// endpoints are handled transparently.
//
// Use errors.Is with the package's sentinel errors to branch on the failure
// class without string-matching:
//
//	session, err := c.GetSession(id)
//	if errors.Is(err, client.ErrNotFound) {
//	    // handle missing session
//	}
//
// Use errors.As to inspect the full error detail:
//
//	var apiErr *client.APIError
//	if errors.As(err, &apiErr) {
//	    log.Printf("status=%d code=%s details=%v", apiErr.Status, apiErr.Code, apiErr.Details)
//	}
package client
