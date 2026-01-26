// Package acp provides ACP (Agent Communication Protocol) client implementation.
package acp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
			idx = idx - 1
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
	// Prefer an allow option if present
	for _, o := range params.Options {
		if o.Kind == acp.PermissionOptionKindAllowOnce || o.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
				},
			}, nil
		}
	}
	// Otherwise choose the first option
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
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
	if !filepath.IsAbs(params.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	dir := filepath.Dir(params.Path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return acp.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", params.Path, err)
	}
	c.print("\n📝 Wrote %d bytes to %s\n", len(params.Content), params.Path)
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *Client) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acp.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", params.Path, err)
	}
	content := string(b)
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = min(max(*params.Line-1, 0), len(lines))
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 {
			if start+*params.Limit < end {
				end = start + *params.Limit
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}
	c.print("\n📖 Read %d bytes from %s\n", len(content), params.Path)
	return acp.ReadTextFileResponse{Content: content}, nil
}
