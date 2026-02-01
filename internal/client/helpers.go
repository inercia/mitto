package client

import (
	"context"
	"fmt"
	"sync"
)

// PromptResult contains the result of a prompt.
type PromptResult struct {
	// Messages contains all agent message HTML received.
	Messages []string

	// Thoughts contains all agent thought text received.
	Thoughts []string

	// ToolCalls contains all tool calls made.
	ToolCalls []ToolCallInfo

	// FilesRead contains all files read by the agent.
	FilesRead []FileInfo

	// FilesWritten contains all files written by the agent.
	FilesWritten []FileInfo

	// EventCount is the total number of events from prompt_complete.
	EventCount int

	// Error contains any error message received.
	Error string
}

// ToolCallInfo contains information about a tool call.
type ToolCallInfo struct {
	ID     string
	Title  string
	Status string
}

// FileInfo contains information about a file operation.
type FileInfo struct {
	Path string
	Size int
}

// PromptAndWait sends a prompt and waits for the response to complete.
// It creates a new connection with capturing callbacks, sends the prompt,
// waits for completion, and returns the result.
// The connection is closed when the function returns.
func (c *Client) PromptAndWait(ctx context.Context, sessionID, message string) (*PromptResult, error) {
	return c.promptAndWaitInternal(ctx, sessionID, message, nil)
}

// PromptAndWaitWithImages sends a prompt with images and waits for completion.
func (c *Client) PromptAndWaitWithImages(ctx context.Context, sessionID, message string, imageIDs []string) (*PromptResult, error) {
	return c.promptAndWaitInternal(ctx, sessionID, message, imageIDs)
}

func (c *Client) promptAndWaitInternal(ctx context.Context, sessionID, message string, imageIDs []string) (*PromptResult, error) {
	result := &PromptResult{}
	var mu sync.Mutex
	done := make(chan struct{})
	connected := make(chan struct{})

	// Connect with capturing callbacks
	sess, err := c.Connect(ctx, sessionID, SessionCallbacks{
		OnConnected: func(sid, clientID, acpServer string) {
			close(connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			result.Messages = append(result.Messages, html)
			mu.Unlock()
		},
		OnAgentThought: func(text string) {
			mu.Lock()
			result.Thoughts = append(result.Thoughts, text)
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			result.ToolCalls = append(result.ToolCalls, ToolCallInfo{
				ID:     id,
				Title:  title,
				Status: status,
			})
			mu.Unlock()
		},
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			for i := range result.ToolCalls {
				if result.ToolCalls[i].ID == id {
					result.ToolCalls[i].Status = status
					break
				}
			}
			mu.Unlock()
		},
		OnFileRead: func(path string, size int) {
			mu.Lock()
			result.FilesRead = append(result.FilesRead, FileInfo{Path: path, Size: size})
			mu.Unlock()
		},
		OnFileWrite: func(path string, size int) {
			mu.Lock()
			result.FilesWritten = append(result.FilesWritten, FileInfo{Path: path, Size: size})
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			result.EventCount = eventCount
			mu.Unlock()
			close(done)
		},
		OnError: func(msg string) {
			mu.Lock()
			result.Error = msg
			mu.Unlock()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer sess.Close()

	// Wait for connection
	select {
	case <-connected:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Send the prompt
	if imageIDs != nil {
		err = sess.SendPromptWithImages(message, imageIDs)
	} else {
		err = sess.SendPrompt(message)
	}
	if err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	// Wait for completion or context cancellation
	select {
	case <-done:
		return result, nil
	case <-ctx.Done():
		return result, ctx.Err()
	}
}
