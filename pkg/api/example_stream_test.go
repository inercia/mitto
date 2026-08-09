package client_test

import (
	"context"
	"fmt"
	"log"

	client "github.com/inercia/mitto/pkg/api"
)

// Example_streaming demonstrates the canonical streaming use case: create a
// session, connect, send a prompt, then range over Events printing agent
// output until the prompt completes (docs/devel/go-client-library.md §6).
//
// This example has no "Output:" comment, so `go test` compiles it (catching
// API drift) without executing it against a live server.
func Example_streaming() {
	ctx := context.Background()
	c := client.New("http://localhost:8080")

	info, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "example-session",
		WorkingDir: "/path/to/project",
	})
	if err != nil {
		log.Fatal(err)
	}

	sess, err := c.Connect(ctx, info.SessionID, client.SessionCallbacks{})
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	if err := sess.SendPrompt("What does this project do?"); err != nil {
		log.Fatal(err)
	}

	for ev, err := range sess.Events(ctx) {
		if err != nil {
			log.Fatal(err) // ctx cancelled, disconnected, or ErrSlowConsumer
		}
		if ev.Kind == client.EventAgentMessage {
			fmt.Print(ev.HTML)
		}
		if ev.Kind == client.EventPromptComplete {
			break
		}
	}
}
