// Command prompt-stream is a runnable example of the Mitto Go client
// (github.com/inercia/mitto/pkg/api) that proves the library is usable from
// an external Go program: it authenticates with a shared bearer token,
// creates a conversation, sends a single prompt, and streams the agent's
// response to stdout as it arrives.
//
//	go run ./examples/go-client/prompt-stream \
//	    -url http://localhost:8080 -token "$MITTO_TOKEN" \
//	    -dir /path/to/project -prompt "What does this project do?"
//
// Press Ctrl-C to cancel early; the connection and the created conversation
// are always cleaned up.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	client "github.com/inercia/mitto/pkg/api"
)

func main() {
	if err := run(); err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "prompt-stream: %s (status=%d code=%s)\n", apiErr.Message, apiErr.Status, apiErr.Code)
		} else {
			fmt.Fprintf(os.Stderr, "prompt-stream: %v\n", err)
		}
		os.Exit(1)
	}
}

func run() error {
	url := flag.String("url", "http://localhost:8080", "Mitto server base URL")
	token := flag.String("token", os.Getenv("MITTO_TOKEN"), "shared bearer token (default: $MITTO_TOKEN)")
	dir := flag.String("dir", ".", "working directory for the new conversation")
	prompt := flag.String("prompt", "What does this project do?", "prompt to send")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall deadline for the exchange")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	var opts []client.Option
	if *token != "" {
		opts = append(opts, client.WithBearerToken(*token))
	}
	c := client.New(*url, opts...)

	info, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "prompt-stream-example",
		WorkingDir: *dir,
	})
	if err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	defer c.DeleteSession(info.SessionID)

	sess, err := c.Connect(ctx, info.SessionID, client.SessionCallbacks{})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer sess.Close()

	if err := sess.SendPrompt(*prompt); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	for ev, err := range sess.Events(ctx) {
		if err != nil {
			return fmt.Errorf("stream: %w", err)
		}
		switch ev.Kind {
		case client.EventAgentMessage:
			fmt.Print(ev.HTML)
		case client.EventPromptComplete:
			fmt.Println()
			return nil
		case client.EventError:
			return fmt.Errorf("agent error: %s", ev.Message)
		}
	}
	return nil
}
