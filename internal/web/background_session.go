package web

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/session"
)

// BackgroundSession manages an ACP session that runs independently of WebSocket connections.
// It continues running even when no client is connected, persisting all events to disk.
// Clients can attach/detach to receive real-time updates.
type BackgroundSession struct {
	// Immutable identifiers
	persistedID string // Session ID for persistence and routing
	acpID       string // ACP protocol session ID

	// ACP connection state
	acpCmd    *exec.Cmd
	acpConn   *acp.ClientSideConnection
	acpClient *WebClient

	// Session persistence
	recorder        *session.Recorder
	agentMsgBuffer  *agentMessageBuffer
	agentThoughtBuf *agentMessageBuffer

	// Session lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Int32

	// Attached client (can be nil when no client is connected)
	clientMu sync.RWMutex
	client   *WSClient

	// Prompt state
	promptMu    sync.Mutex
	isPrompting bool
	promptCount int

	// Permission handling when no client is attached
	permissionMu   sync.Mutex
	permissionChan chan acp.RequestPermissionResponse

	// Configuration
	autoApprove bool
	logger      *slog.Logger
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID string
	ACPCommand  string
	ACPServer   string
	WorkingDir  string
	AutoApprove bool
	Logger      *slog.Logger
	Store       *session.Store
	SessionName string
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
func NewBackgroundSession(config BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:             ctx,
		cancel:          cancel,
		autoApprove:     config.AutoApprove,
		logger:          config.Logger,
		agentMsgBuffer:  &agentMessageBuffer{},
		agentThoughtBuf: &agentMessageBuffer{},
		permissionChan:  make(chan acp.RequestPermissionResponse, 1),
	}

	// Create recorder for persistence
	if config.Store != nil {
		bs.recorder = session.NewRecorder(config.Store)
		bs.persistedID = bs.recorder.SessionID()
		if err := bs.recorder.Start(config.ACPServer, config.WorkingDir); err != nil {
			cancel()
			return nil, err
		}
		// Update session name
		if config.SessionName != "" {
			config.Store.UpdateMetadata(bs.persistedID, func(meta *session.Metadata) {
				meta.Name = config.SessionName
			})
		}
	}

	// Start ACP process
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
	}

	return bs, nil
}

// GetSessionID returns the persisted session ID.
func (bs *BackgroundSession) GetSessionID() string {
	return bs.persistedID
}

// GetACPID returns the ACP protocol session ID.
func (bs *BackgroundSession) GetACPID() string {
	return bs.acpID
}

// IsClosed returns true if the session has been closed.
func (bs *BackgroundSession) IsClosed() bool {
	return bs.closed.Load() != 0
}

// IsPrompting returns true if a prompt is currently being processed.
func (bs *BackgroundSession) IsPrompting() bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.isPrompting
}

// GetPromptCount returns the number of prompts sent in this session.
func (bs *BackgroundSession) GetPromptCount() int {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.promptCount
}

// CreatedAt returns when the session was created.
func (bs *BackgroundSession) CreatedAt() time.Time {
	if bs.recorder != nil {
		// This would need to be stored; for now return zero
		return time.Time{}
	}
	return time.Time{}
}

// AttachClient attaches a WebSocket client to receive real-time updates.
// Only one client can be attached at a time.
func (bs *BackgroundSession) AttachClient(client *WSClient) {
	bs.clientMu.Lock()
	defer bs.clientMu.Unlock()
	bs.client = client
}

// DetachClient detaches the current client.
func (bs *BackgroundSession) DetachClient() {
	bs.clientMu.Lock()
	defer bs.clientMu.Unlock()
	bs.client = nil
}

// GetAttachedClient returns the currently attached client, or nil.
func (bs *BackgroundSession) GetAttachedClient() *WSClient {
	bs.clientMu.RLock()
	defer bs.clientMu.RUnlock()
	return bs.client
}

// HasAttachedClient returns true if a client is currently attached.
func (bs *BackgroundSession) HasAttachedClient() bool {
	return bs.GetAttachedClient() != nil
}

// Close shuts down the session and releases all resources.
func (bs *BackgroundSession) Close(reason string) {
	if !bs.closed.CompareAndSwap(0, 1) {
		return // Already closed
	}

	// Cancel context to stop any ongoing operations
	bs.cancel()

	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Close ACP client
	if bs.acpClient != nil {
		bs.acpClient.Close()
	}

	// Kill ACP process
	if bs.acpCmd != nil && bs.acpCmd.Process != nil {
		bs.acpCmd.Process.Kill()
	}

	// End recording
	if bs.recorder != nil {
		bs.recorder.End(reason)
	}
}

// Suspend suspends the session without ending it (for temporary disconnects).
func (bs *BackgroundSession) Suspend() {
	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Suspend recording (keeps status as "active")
	if bs.recorder != nil {
		bs.recorder.Suspend()
	}
}

// flushAndPersistMessages persists any buffered agent messages.
func (bs *BackgroundSession) flushAndPersistMessages() {
	if bs.recorder == nil {
		return
	}

	if bs.agentMsgBuffer != nil {
		text := bs.agentMsgBuffer.Flush()
		if text != "" {
			if err := bs.recorder.RecordAgentMessage(text); err != nil && bs.logger != nil {
				bs.logger.Error("Failed to persist agent message", "error", err)
			}
		}
	}

	if bs.agentThoughtBuf != nil {
		text := bs.agentThoughtBuf.Flush()
		if text != "" {
			if err := bs.recorder.RecordAgentThought(text); err != nil && bs.logger != nil {
				bs.logger.Error("Failed to persist agent thought", "error", err)
			}
		}
	}
}

// startACPProcess starts the ACP server process and initializes the connection.
func (bs *BackgroundSession) startACPProcess(acpCommand, workingDir string) error {
	args := strings.Fields(acpCommand)
	if len(args) == 0 {
		return &sessionError{"empty ACP command"}
	}

	cmd := exec.CommandContext(bs.ctx, args[0], args[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return &sessionError{"failed to create stdin pipe: " + err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &sessionError{"failed to create stdout pipe: " + err.Error()}
	}

	if err := cmd.Start(); err != nil {
		return &sessionError{"failed to start ACP server: " + err.Error()}
	}

	bs.acpCmd = cmd

	// Create web client with callbacks that route to attached client or persist
	bs.acpClient = NewWebClient(WebClientConfig{
		AutoApprove:    bs.autoApprove,
		OnAgentMessage: bs.onAgentMessage,
		OnAgentThought: bs.onAgentThought,
		OnToolCall:     bs.onToolCall,
		OnToolUpdate:   bs.onToolUpdate,
		OnPlan:         bs.onPlan,
		OnFileWrite:    bs.onFileWrite,
		OnFileRead:     bs.onFileRead,
		OnPermission:   bs.onPermission,
	})

	// Create ACP connection
	bs.acpConn = acp.NewClientSideConnection(bs.acpClient, stdin, stdout)
	if bs.logger != nil {
		bs.acpConn.SetLogger(bs.logger)
	}

	// Initialize
	_, err = bs.acpConn.Initialize(bs.ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		bs.acpCmd.Process.Kill()
		return &sessionError{"failed to initialize: " + err.Error()}
	}

	// Create session
	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}
	sessResp, err := bs.acpConn.NewSession(bs.ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		bs.acpCmd.Process.Kill()
		return &sessionError{"failed to create session: " + err.Error()}
	}

	bs.acpID = string(sessResp.SessionId)
	return nil
}

// sessionError is a simple error type for session errors.
type sessionError struct {
	msg string
}

func (e *sessionError) Error() string {
	return e.msg
}

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil {
		return &sessionError{"no ACP connection"}
	}

	bs.promptMu.Lock()
	if bs.isPrompting {
		bs.promptMu.Unlock()
		return &sessionError{"prompt already in progress"}
	}
	bs.isPrompting = true
	bs.promptCount++
	isFirstPrompt := bs.promptCount == 1
	bs.promptMu.Unlock()

	// Persist user prompt
	if bs.recorder != nil {
		bs.recorder.RecordUserPrompt(message)
	}

	// Run prompt in background
	go func() {
		defer func() {
			bs.promptMu.Lock()
			bs.isPrompting = false
			bs.promptMu.Unlock()
		}()

		_, err := bs.acpConn.Prompt(bs.ctx, acp.PromptRequest{
			SessionId: acp.SessionId(bs.acpID),
			Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
		})

		if bs.IsClosed() {
			return
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
		}

		// Persist buffered messages
		bs.flushAndPersistMessages()

		// Notify attached client
		client := bs.GetAttachedClient()
		if client != nil {
			if err != nil {
				client.sendError("Prompt failed: " + err.Error())
			} else {
				client.sendMessage(WSMsgTypePromptComplete, map[string]interface{}{
					"session_id": bs.persistedID,
				})

				// Auto-generate title after first prompt
				if isFirstPrompt && bs.persistedID != "" {
					client.generateAndSetTitle(message, bs.persistedID)
				}
			}
		}
	}()

	return nil
}

// Cancel cancels the current prompt.
func (bs *BackgroundSession) Cancel() error {
	if bs.acpConn == nil {
		return nil
	}
	return bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
		SessionId: acp.SessionId(bs.acpID),
	})
}

// --- Callback methods for WebClient ---

func (bs *BackgroundSession) onAgentMessage(html string) {
	if bs.IsClosed() {
		return
	}

	// Buffer for persistence
	if bs.agentMsgBuffer != nil {
		bs.agentMsgBuffer.Write(html)
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeAgentMessage, map[string]interface{}{
			"html":       html,
			"format":     "html",
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onAgentThought(text string) {
	if bs.IsClosed() {
		return
	}

	// Buffer for persistence
	if bs.agentThoughtBuf != nil {
		bs.agentThoughtBuf.Write(text)
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeAgentThought, map[string]interface{}{
			"text":       text,
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onToolCall(id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Persist
	if bs.recorder != nil {
		bs.recorder.RecordToolCall(id, title, status, "", nil, nil)
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeToolCall, map[string]interface{}{
			"id":         id,
			"title":      title,
			"status":     status,
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onToolUpdate(id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Persist
	if bs.recorder != nil {
		bs.recorder.RecordToolCallUpdate(id, status, nil)
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		data := map[string]interface{}{
			"id":         id,
			"session_id": bs.persistedID,
		}
		if status != nil {
			data["status"] = *status
		}
		client.sendMessage(WSMsgTypeToolUpdate, data)
	}
}

func (bs *BackgroundSession) onPlan() {
	if bs.IsClosed() {
		return
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypePlan, map[string]interface{}{
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onFileWrite(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeFileWrite, map[string]interface{}{
			"path":       path,
			"size":       size,
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onFileRead(path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Forward to attached client
	if client := bs.GetAttachedClient(); client != nil {
		client.sendMessage(WSMsgTypeFileRead, map[string]interface{}{
			"path":       path,
			"size":       size,
			"session_id": bs.persistedID,
		})
	}
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if bs.IsClosed() {
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	client := bs.GetAttachedClient()

	// If no client is attached, auto-approve if configured
	if client == nil {
		if bs.autoApprove {
			// Find an "allow" option
			for _, opt := range params.Options {
				if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
					if bs.recorder != nil {
						bs.recorder.RecordPermission(title, string(opt.OptionId), "auto_approved_no_client")
					}
					return acp.RequestPermissionResponse{
						Outcome: acp.RequestPermissionOutcome{
							Selected: &acp.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
						},
					}, nil
				}
			}
			// Default to first option
			if len(params.Options) > 0 {
				if bs.recorder != nil {
					bs.recorder.RecordPermission(title, string(params.Options[0].OptionId), "auto_approved_no_client")
				}
				return acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
					},
				}, nil
			}
		}
		// No client and no auto-approve - cancel
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
		}, nil
	}

	// Forward to attached client - format options for frontend
	options := make([]map[string]string, len(params.Options))
	for i, opt := range params.Options {
		options[i] = map[string]string{
			"id":   string(opt.OptionId),
			"name": opt.Name,
			"kind": string(opt.Kind),
		}
	}

	client.sendMessage(WSMsgTypePermission, map[string]interface{}{
		"title":      title,
		"options":    options,
		"session_id": bs.persistedID,
	})

	// Wait for response from client
	bs.permissionMu.Lock()
	// Clear any stale response
	select {
	case <-bs.permissionChan:
	default:
	}
	bs.permissionMu.Unlock()

	select {
	case <-ctx.Done():
		return acp.RequestPermissionResponse{}, ctx.Err()
	case <-bs.ctx.Done():
		return acp.RequestPermissionResponse{}, bs.ctx.Err()
	case resp := <-bs.permissionChan:
		// Persist the permission decision
		if bs.recorder != nil {
			if resp.Outcome.Selected != nil {
				bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "user_selected")
			} else {
				bs.recorder.RecordPermission(title, "", "cancelled")
			}
		}
		return resp, nil
	}
}

// AnswerPermission provides a response to a pending permission request.
func (bs *BackgroundSession) AnswerPermission(optionID string, cancel bool) {
	var resp acp.RequestPermissionResponse
	if cancel {
		resp.Outcome.Cancelled = &acp.RequestPermissionOutcomeCancelled{}
	} else {
		resp.Outcome.Selected = &acp.RequestPermissionOutcomeSelected{
			OptionId: acp.PermissionOptionId(optionID),
		}
	}

	select {
	case bs.permissionChan <- resp:
	default:
	}
}
