// Package acp provides ACP (Agent Communication Protocol) client implementation.
package acp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// Client implements the acp.Client interface for interacting with ACP agents.
type Client struct {
	autoApprove bool
	output      func(msg string)
}

// Ensure Client implements acp.Client
var _ acp.Client = (*Client)(nil)

// NewClient creates a new ACP client.
func NewClient(autoApprove bool, output func(msg string)) *Client {
	return &Client{
		autoApprove: autoApprove,
		output:      output,
	}
}

func (c *Client) print(format string, args ...any) {
	if c.output != nil {
		c.output(fmt.Sprintf(format, args...))
	}
}

// RequestPermission handles permission requests from the agent.
func (c *Client) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if c.autoApprove {
		return c.autoApprovePermission(params)
	}

	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	c.print("\n🔐 Permission requested: %s\n", title)
	c.print("\nOptions:\n")
	for i, opt := range params.Options {
		c.print("   %d. %s (%s)\n", i+1, opt.Name, opt.Kind)
	}

	// Use a channel to receive input from stdin in a non-blocking way
	inputChan := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			inputChan <- strings.TrimSpace(line)
		}
	}()

	for {
		c.print("\nChoose an option (or Ctrl+C to cancel): ")

		select {
		case <-ctx.Done():
			c.print("\n🛑 Permission request cancelled\n")
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
			}, ctx.Err()
		case line := <-inputChan:
			if line == "" {
				continue
			}
			idx := -1
			_, _ = fmt.Sscanf(line, "%d", &idx)
			idx--
			if idx >= 0 && idx < len(params.Options) {
				return acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[idx].OptionId},
					},
				}, nil
			}
			c.print("Invalid option. Please try again.\n")
		}
	}
}

// autoApprovePermission automatically approves permission requests.
func (c *Client) autoApprovePermission(params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return AutoApprovePermission(params.Options), nil
}

// SessionUpdate handles session updates (streaming output) from the agent.
func (c *Client) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.print("%s", content.Text.Text)
		}
	case u.ToolCall != nil:
		c.print("\n🔧 %s (%s)\n", u.ToolCall.Title, u.ToolCall.Status)
	case u.ToolCallUpdate != nil:
		if u.ToolCallUpdate.Status != nil {
			c.print("\n🔧 Tool call updated: %v\n", *u.ToolCallUpdate.Status)
		}
	case u.Plan != nil:
		c.print("\n📋 [plan update]\n")
	case u.AgentThoughtChunk != nil:
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil {
			c.print("💭 %s", thought.Text.Text)
		}
	}
	return nil
}

// WriteTextFile handles file write requests from the agent.
func (c *Client) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := DefaultFileSystem.WriteTextFile(params.Path, params.Content); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	c.print("\n📝 Wrote %d bytes to %s\n", len(params.Content), params.Path)
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *Client) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	content, err := DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	c.print("\n📖 Read %d bytes from %s\n", len(content), params.Path)
	return acp.ReadTextFileResponse{Content: content}, nil
}
