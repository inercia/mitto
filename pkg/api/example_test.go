package client_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	client "github.com/inercia/mitto/pkg/api"
)

// Example_listSessions demonstrates the simplest possible use of the
// client: an unauthenticated GET against a server with no auth configured
// (docs.go "Basic Usage").
//
// No "Output:" comment, so `go test` compiles this (catching API drift)
// without executing it against a live server.
func Example_listSessions() {
	c := client.New("http://localhost:8080")

	sessions, err := c.ListSessions()
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range sessions {
		fmt.Println(s.SessionID, s.Status)
	}
}

// Example_bearerToken demonstrates constructing a Client authenticated with
// a shared bearer token sourced lazily from the environment, so it can
// rotate without reconstructing the Client (doc.go "Authentication").
func Example_bearerToken() {
	c := client.New("http://localhost:8080", client.WithTokenSupplier(func() (string, error) {
		return os.Getenv("MITTO_TOKEN"), nil
	}))

	if _, err := c.ListSessions(); err != nil {
		log.Fatal(err)
	}
}

// Example_promptAndWait demonstrates the simplified request-response helper
// for callers that don't need incremental streaming (doc.go "Simplified
// Prompt Helper").
func Example_promptAndWait() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := client.New("http://localhost:8080")
	result, err := c.PromptAndWait(ctx, "some-session-id", "Explain this code")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("got %d messages, %d tool calls\n", len(result.Messages), len(result.ToolCalls))
}

// Example_errorHandling demonstrates branching on the failure class with
// errors.Is (sentinel comparison by HTTP status) and recovering full detail
// with errors.As (doc.go "Errors").
func Example_errorHandling() {
	c := client.New("http://localhost:8080")

	_, err := c.GetSession("missing-session-id")
	if errors.Is(err, client.ErrNotFound) {
		fmt.Println("session not found")
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		log.Printf("status=%d code=%s details=%v", apiErr.Status, apiErr.Code, apiErr.Details)
	}
}

// Example_reconnect demonstrates opting into the browser-parity resilience
// behavior on Connect: automatic reconnection, keepalive-based zombie
// detection, and sequence-number dedup (doc.go "Resilient Realtime").
func Example_reconnect() {
	ctx := context.Background()
	c := client.New("http://localhost:8080")

	sess, err := c.Connect(ctx, "some-session-id", client.SessionCallbacks{},
		client.WithReconnect(client.ReconnectConfig{}),
		client.WithKeepalive(client.KeepaliveConfig{}),
		client.WithSeqDedup(true),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()
}
