package conversation

// Prompt dispatch cluster for BackgroundSession.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// buildPromptWithHistory prepends stored conversation history to the prompt for resumed sessions.
func (bs *BackgroundSession) buildPromptWithHistory(message string) string {
	if bs.store == nil {
		return message
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read events for history injection", "error", err)
		}
		return message
	}

	// Build conversation history (limit to last 5 turns to avoid token limits)
	history := session.BuildConversationHistory(events, 5)
	if history == "" {
		return message
	}

	if bs.logger != nil {
		bs.logger.Debug("Injecting conversation history into resumed session",
			"history_length", len(history))
	}

	return history + message
}

// SetPromptResolver sets the function used to resolve named workspace prompts to their full text.
// This is called by the server setup code (same resolver used by PeriodicRunner).
func (bs *BackgroundSession) SetPromptResolver(resolver PromptResolver) {
	bs.promptResolver = resolver
}

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID         string          // Unique identifier of the sending client (for broadcast deduplication)
	PromptID         string          // Client-generated prompt ID (for delivery confirmation)
	PromptName       string          // Name of workspace prompt (resolved to full text before ACP; empty for ad-hoc prompts)
	ImageIDs         []string        // IDs of images attached to the prompt
	FileIDs          []string        // IDs of files attached to the prompt
	OnComplete       func(err error) // Called when the async prompt goroutine finishes (nil = success)
	IsPeriodicForced bool            // True when this periodic prompt was triggered manually via "run now"
	FreshContext     bool            // True to suppress history injection and use a new ACP session for this prompt
	// Arguments, when non-empty, triggers bash-like ${VAR}/${VAR:-default}
	// substitution on the resolved prompt text before persistence and broadcast.
	// Only set for named/scenario prompts; ad-hoc messages leave this nil so that
	// pasted shell/code containing ${...} is never corrupted.
	Arguments map[string]string
	// PreferredModels is an ordered list of case-insensitive glob patterns matched against
	// available model IDs and display names. The first match wins; absent/empty uses the
	// session's baseline model. When empty and PromptName is set, the list is resolved
	// from the prompt definition via preferredModelsResolver inside PromptWithMeta.
	PreferredModels []string
	// Meta is an optional generic metadata bag attached to the persisted user-prompt
	// event. Same sensitivity rules as session.RecordOption apply: no secrets,
	// credentials, full argument values, or full prompt text.
	// When non-empty, the bag is forwarded to EventMetaObserver.OnEventMeta so it
	// can flow through to the WebSocket payload without per-field wiring.
	Meta map[string]any
}

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	return bs.PromptWithMeta(message, PromptMeta{})
}

// PromptWithImages sends a message with optional images to the agent. This runs asynchronously.
// The imageIDs should be IDs of images previously uploaded to this session.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithImages(message string, imageIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs})
}

// PromptWithAttachments sends a message with optional images and files to the agent.
// This runs asynchronously. The IDs should be of previously uploaded images/files.
func (bs *BackgroundSession) PromptWithAttachments(message string, imageIDs, fileIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs, FileIDs: fileIDs})
}

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	// Resolve prompt name to full text before any other processing.
	// meta.PromptName is UI metadata only; the ACP agent always receives the full text.
	if meta.PromptName != "" && message == "" {
		if bs.promptResolver == nil {
			return fmt.Errorf("prompt %q cannot be resolved: no prompt resolver configured", meta.PromptName)
		}
		resolved, err := bs.promptResolver(meta.PromptName, bs.workingDir)
		if err != nil {
			return fmt.Errorf("failed to resolve prompt %q: %w", meta.PromptName, err)
		}
		message = resolved
	}

	// Capture argument count before substitution (count is the number of distinct
	// ${VAR} arguments provided, not the number of substitution sites in the text).
	argCount := len(meta.Arguments)

	// Apply bash-like ${VAR}/${VAR:-default} argument substitution when the caller
	// supplied an arguments map. Done here (the single chokepoint for all entry
	// paths) and before persistence/broadcast so the transcript shows the
	// substituted text. Guarded on len > 0 so ad-hoc messages are untouched.
	if argCount > 0 {
		message = processors.SubstituteArguments(message, meta.Arguments)
	}

	// Record the argument names (keys only, sorted) as a generic meta annotation so
	// the conversation can surface which parameters were filled. Names are safe
	// identifiers; values are substituted into the prompt text above and must never
	// enter the meta bag (sensitivity policy).
	if argCount > 0 {
		names := make([]string, 0, len(meta.Arguments))
		for k := range meta.Arguments {
			names = append(names, k)
		}
		sort.Strings(names)
		if meta.Meta == nil {
			meta.Meta = make(map[string]any)
		}
		meta.Meta["argument_names"] = names
	}

	imageIDs := meta.ImageIDs
	fileIDs := meta.FileIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil && bs.sharedProcess == nil {
		return &sessionError{"The AI agent is still starting up. Please wait a moment and try again."}
	}

retryAfterRestart:
	bs.promptMu.Lock()
	if bs.isPrompting {
		// Check if the ACP connection is dead (process crashed)
		// We use non-blocking checks on both Done() and acpProcessDone channels.
		// acpProcessDone fires faster than Done() because it uses OS-level process
		// liveness checks rather than waiting for pipe EOF propagation.
		acpDead := false
		if bs.acpConn != nil {
			select {
			case <-bs.acpConn.Done():
				acpDead = true
			default:
				// Connection still alive
			}
		} else if bs.sharedProcess != nil {
			select {
			case <-bs.sharedProcess.Done():
				acpDead = true
			default:
				// Shared connection still alive
			}
		} else {
			acpDead = true // No connection at all
		}
		// Also check OS-level process death (faster detection)
		if !acpDead && bs.acpProcessDone != nil {
			select {
			case <-bs.acpProcessDone:
				acpDead = true
			default:
			}
		}

		if acpDead {
			elapsed := time.Since(bs.promptStartTime)
			if bs.logger != nil {
				bs.logger.Warn("Detected dead ACP connection",
					"prompt_start_time", bs.promptStartTime,
					"elapsed", elapsed)
			}
			bs.isPrompting = false
			bs.lastResponseComplete = time.Now()
			bs.promptMu.Unlock()

			// Check if we can restart automatically
			if bs.canRestartACP() {
				// Notify observers that we're restarting (include attempt count so
				// the user understands this is a retry loop, not a one-off)
				restartInfo := bs.getRestartInfo()
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
				})

				// Attempt to restart the ACP process
				if err := bs.restartACPProcess(RestartReasonCrashDuringPrompt); err != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + err.Error() + ". Please switch to another conversation and back to retry."
					if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = formatClassifiedError(classified)
					}
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError(errMsg)
					})
					return &sessionError{"ACP process died and restart failed: " + err.Error()}
				}

				// Restart succeeded — automatically retry the prompt.
				// Note: we say "restarted" (not "restarted successfully") because the
				// process may crash again on the next prompt — we don't want to give
				// false confidence.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Retrying your message automatically...")
				})
				if bs.logger != nil {
					bs.logger.Info("Auto-retrying prompt after ACP restart",
						"session_id", bs.persistedID,
						"reason", "crash_during_prompt")
				}
				// isPrompting was cleared above; re-acquire promptMu and proceed
				// through the normal prompt path below.
				goto retryAfterRestart
			}

			// Restart limit exceeded - notify user to manually restart
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
			})
			return &sessionError{"ACP process died repeatedly - switch conversations to restart"}
		} else {
			bs.promptMu.Unlock()
			return &sessionError{"prompt already in progress"}
		}
	}
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptCount++
	bs.TouchActivity()

	// Check if we need to inject conversation history (first prompt of resumed session).
	// FreshContext suppresses history injection so each periodic run starts clean.
	shouldInjectHistory := bs.isResumed && !bs.historyInjected && !meta.FreshContext
	if shouldInjectHistory {
		bs.historyInjected = true
	}

	// Capture first prompt state for message processors
	isFirst := bs.isFirstPrompt
	if isFirst {
		bs.isFirstPrompt = false
	}
	bs.promptMu.Unlock()

	// Notify about streaming state change (prompt started)
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, true)
	}

	// Load images and build content blocks
	var imageRefs []session.ImageRef
	var contentBlocks []acp.ContentBlock

	if len(imageIDs) > 0 && !bs.agentSupportsImages {
		if bs.logger != nil {
			bs.logger.Warn("Agent did not advertise image support, sending images anyway",
				"image_count", len(imageIDs),
				"session_id", bs.persistedID)
		}
		// Warn the user but still send images — models sometimes misreport capabilities
		bs.notifyObservers(func(o SessionObserver) {
			o.OnError("⚠️ The current AI agent did not advertise image support. " +
				"Images will be sent anyway, but may not be processed correctly.")
		})
	}

	if len(imageIDs) > 0 && bs.store != nil {
		for _, imageID := range imageIDs {
			imagePath, err := bs.store.GetImagePath(bs.persistedID, imageID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get image path", "image_id", imageID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(imageID, "."); idx >= 0 {
				ext = imageID[idx:]
			}
			mimeType := session.GetMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "image/png" // Default fallback
			}

			// Load image and create attachment
			att, err := mittoAcp.ImageAttachmentFromFile(imagePath, mimeType)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to load image", "image_id", imageID, "error", err)
				}
				continue
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			imageRefs = append(imageRefs, session.ImageRef{
				ID:       imageID,
				MimeType: mimeType,
			})
		}
	}

	// Load files and build content blocks
	var fileRefs []session.FileRef
	if len(fileIDs) > 0 && bs.store != nil {
		for _, fileID := range fileIDs {
			filePath, err := bs.store.GetFilePath(bs.persistedID, fileID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get file path", "file_id", fileID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(fileID, "."); idx >= 0 {
				ext = fileID[idx:]
			}
			mimeType := session.GetFileMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			// Determine file category and create appropriate attachment
			category := session.GetFileCategory(mimeType)
			var att mittoAcp.Attachment
			if category == session.FileCategoryText {
				// Text files are embedded inline
				att, err = mittoAcp.TextFileAttachmentFromFile(filePath, mimeType)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Warn("Failed to load text file", "file_id", fileID, "error", err)
					}
					continue
				}
			} else {
				// Binary files are referenced by path
				att = mittoAcp.BinaryFileAttachment(filePath, mimeType)
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			fileRefs = append(fileRefs, session.FileRef{
				ID:       fileID,
				Name:     att.Name,
				MimeType: mimeType,
				Category: category,
			})
		}
	}

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Clear cached plan state when new prompt starts
	// The existing plan becomes stale; a new plan will be generated for this prompt
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, nil)
	}

	// Persist user prompt with image/file references and prompt ID
	// User prompts are persisted immediately (not buffered), so we need to
	// refresh nextSeq after persistence to get the correct seq for the prompt
	// The prompt ID is included so clients can clear pending prompts on reconnect
	var userPromptSeq int64
	if bs.recorder != nil {
		var recordOpts []session.RecordOption
		if len(meta.Meta) > 0 {
			recordOpts = append(recordOpts, session.WithMetaMap(meta.Meta))
		}
		if err := bs.recorder.RecordUserPromptComplete(message, imageRefs, fileRefs, meta.PromptID, meta.PromptName, argCount, recordOpts...); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
		// Get the seq that was assigned to the user prompt (it's the current event count)
		userPromptSeq = int64(bs.recorder.EventCount())
		// Update nextSeq for subsequent agent events
		bs.refreshNextSeq()
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	fileIDStrings := make([]string, len(fileRefs))
	for i, f := range fileRefs {
		fileIDStrings[i] = f.ID
	}

	// Propagate generic event metadata to observers that implement EventMetaObserver.
	// This must happen BEFORE OnUserPrompt so observers can store the meta keyed by seq
	// and attach it to the outgoing payload inside OnUserPrompt.
	if userPromptSeq > 0 && len(meta.Meta) > 0 {
		eventMeta := meta.Meta
		bs.notifyObservers(func(o SessionObserver) {
			if m, ok := o.(EventMetaObserver); ok {
				m.OnEventMeta(userPromptSeq, eventMeta)
			}
		})
	}

	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings, meta.PromptName, argCount)
	})

	// Build the actual prompt to send to ACP.
	// Apply the unified processor pipeline (text-mode + command-mode in priority order).
	promptMessage := message
	var procAttachmentBlocks []acp.ContentBlock

	// Fetch session metadata for @mitto:variable substitution.
	// Done unconditionally so substitution works even with no processors configured.
	// Best-effort: unavailable fields substitute to "".
	var sessionName, acpServer, parentSessionID, parentSessionName, beadsIssue string
	var childSessions []processors.ChildSession
	var advancedSettings map[string]bool
	if bs.store != nil && bs.persistedID != "" {
		if sessionMeta, metaErr := bs.store.GetMetadata(bs.persistedID); metaErr == nil {
			sessionName = sessionMeta.Name
			acpServer = sessionMeta.ACPServer
			parentSessionID = sessionMeta.ParentSessionID
			advancedSettings = sessionMeta.AdvancedSettings
			beadsIssue = sessionMeta.BeadsIssue
		}
		// Resolve parent session name for @mitto:parent variable
		if parentSessionID != "" {
			if parentMeta, parentErr := bs.store.GetMetadata(parentSessionID); parentErr == nil {
				parentSessionName = parentMeta.Name
			}
		}
		// Resolve child sessions for @mitto:children variable
		if children, childErr := bs.store.ListChildSessions(bs.persistedID); childErr == nil {
			for _, child := range children {
				isPrompting := false
				if bs.isChildPrompting != nil {
					isPrompting = bs.isChildPrompting(child.SessionID)
				}
				childSessions = append(childSessions, processors.ChildSession{
					ID:          child.SessionID,
					Name:        child.Name,
					ACPServer:   child.ACPServer,
					IsAutoChild: child.ChildOrigin == session.ChildOriginAuto,
					ChildOrigin: string(child.ChildOrigin),
					IsPrompting: isPrompting,
				})
			}
		}
	}
	// Get cached MCP tool names for tools.* CEL context
	var mcpToolNames []string
	if bs.auxiliaryManager != nil && bs.workspaceUUID != "" {
		if tools, ok := bs.auxiliaryManager.GetCachedMCPTools(bs.workspaceUUID); ok {
			mcpToolNames = make([]string, len(tools))
			for i, tool := range tools {
				mcpToolNames[i] = tool.Name
			}
		}
	}

	// Populate user data schema and current user data for processor variables
	var hasUserDataSchema bool
	var hasMittoRC bool
	var hasMetadataDescription bool
	var userDataSchemaJSON string
	var userDataJSON string
	if bs.workingDir != "" {
		rc, rcErr := config.LoadWorkspaceRC(bs.workingDir)
		if rcErr == nil && rc != nil &&
			rc.Metadata != nil && rc.Metadata.UserDataSchema != nil && len(rc.Metadata.UserDataSchema.Fields) > 0 {
			hasUserDataSchema = true
			if schemaBytes, err := json.Marshal(rc.Metadata.UserDataSchema.Fields); err == nil {
				userDataSchemaJSON = string(schemaBytes)
			}
		}
		// Check if .mittorc exists (regardless of content)
		if rcPath, _, err := config.FindWorkspaceRCPath(bs.workingDir); err == nil && rcPath != "" {
			hasMittoRC = true
		}
		// Check if metadata description is set
		if rcErr == nil && rc != nil && rc.Metadata != nil && rc.Metadata.Description != "" {
			hasMetadataDescription = true
		}
	}
	if bs.store != nil && bs.persistedID != "" {
		if ud, err := bs.store.GetUserData(bs.persistedID); err == nil && ud != nil && len(ud.Attributes) > 0 {
			if udBytes, err := json.Marshal(ud.Attributes); err == nil {
				userDataJSON = string(udBytes)
			}
		}
	}

	processorInput := &processors.ProcessorInput{
		Message:                message,
		IsFirstMessage:         isFirst,
		SessionID:              bs.persistedID,
		WorkingDir:             bs.workingDir,
		ParentSessionID:        parentSessionID,
		ParentSessionName:      parentSessionName,
		SessionName:            sessionName,
		ACPServer:              acpServer,
		WorkspaceUUID:          bs.workspaceUUID,
		BeadsIssue:             beadsIssue,
		AvailableACPServers:    bs.availableACPServers,
		ChildSessions:          childSessions,
		MCPToolNames:           mcpToolNames,
		IsPeriodic:             meta.SenderID == "periodic-runner",
		IsPeriodicForced:       meta.IsPeriodicForced,
		AdvancedSettings:       advancedSettings,
		HasUserDataSchema:      hasUserDataSchema,
		HasMittoRC:             hasMittoRC,
		HasMetadataDescription: hasMetadataDescription,
		UserDataSchemaJSON:     userDataSchemaJSON,
		UserDataJSON:           userDataJSON,
	}

	if bs.processorManager != nil {
		procResult, procErr := bs.processorManager.Apply(bs.ctx, processorInput)
		if procErr != nil {
			if bs.logger != nil {
				bs.logger.Error("Processor execution failed", "error", procErr)
			}
			// Continue with original message on processor failure
		} else {
			// Persist processor activation count to metadata after each successful Apply
			if bs.store != nil && bs.persistedID != "" {
				_, procActivations, procLastAt, _ := bs.GetProcessorStats()
				_ = bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
					m.ProcessorActivations = procActivations
					m.ProcessorLastActivation = procLastAt
				})
			}
		}
		if procResult != nil {
			promptMessage = procResult.Message

			// Convert processor attachments to content blocks
			if len(procResult.Attachments) > 0 {
				acpAttachments, err := procResult.ToACPAttachments(bs.workingDir)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Error("Failed to resolve processor attachments", "error", err)
					}
				} else {
					for _, att := range acpAttachments {
						if att.Type == "image" {
							procAttachmentBlocks = append(procAttachmentBlocks, acp.ImageBlock(att.Data, att.MimeType))
						}
						// Note: Non-image attachments could be handled differently in the future
					}
				}
			}
		}
	}

	// Apply @mitto:variable substitution unconditionally on the assembled message.
	// This covers both the case where processors ran (substitution on assembled output)
	// and the case where no processors are configured (substitution on the raw user message).
	promptMessage = processors.SubstituteVariables(promptMessage, processorInput)

	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(promptMessage)
	}

	// Build final content blocks: images first (from uploads and processors), then text
	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+len(procAttachmentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, procAttachmentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	// Log content block summary for debugging image delivery issues
	if bs.logger != nil {
		var imageBlockCount, textBlockCount, otherBlockCount int
		for _, block := range finalBlocks {
			if block.Image != nil {
				imageBlockCount++
			} else if block.Text != nil {
				textBlockCount++
			} else {
				otherBlockCount++
			}
		}
		bs.logger.Info("Sending prompt to ACP agent",
			"total_blocks", len(finalBlocks),
			"image_blocks", imageBlockCount,
			"text_blocks", textBlockCount,
			"other_blocks", otherBlockCount,
			"processor_attachment_blocks", len(procAttachmentBlocks),
			"agent_supports_images", bs.agentSupportsImages,
			"session_id", bs.persistedID)
	}

	// Run prompt in background
	go func() {
		// autoRetried guards a single automatic retry after an ACP crash during
		// streaming. On the first crash we restart the process and jump back to
		// retryPrompt; if the retry also crashes we fall through to the normal
		// "please resend" message instead of looping forever.
		autoRetried := false

		// For shared-process sessions, complete the deferred session/new handshake
		// before the first prompt. This runs after the HTTP create path has already
		// returned, so a busy agent delays the prompt — not conversation creation.
		// The background prewarm (see PrewarmACPSession) may have already completed
		// this when the client opened the conversation; completeDeferredHandshake is
		// idempotent and a no-op in that case.
		if bs.sharedProcess != nil {
			const maxHandshakeAttempts = 3
			var handshakeErr error
			for attempt := 1; attempt <= maxHandshakeAttempts; attempt++ {
				handshakeErr = bs.completeDeferredHandshake()
				if handshakeErr == nil {
					break
				}
				errStr := strings.ToLower(handshakeErr.Error())
				transient := strings.Contains(errStr, "deadline") ||
					strings.Contains(errStr, "timeout") ||
					strings.Contains(errStr, "timed out")
				if !transient || attempt == maxHandshakeAttempts {
					break
				}
				if bs.logger != nil {
					bs.logger.Warn("Deferred session/new transient failure, retrying",
						"session_id", bs.persistedID,
						"attempt", attempt,
						"error", handshakeErr)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if handshakeErr != nil {
				if bs.logger != nil {
					bs.logger.Error("Deferred session/new failed",
						"session_id", bs.persistedID,
						"error", handshakeErr)
				}
				friendlyMsg := "Could not start the agent session: " + formatACPError(handshakeErr) + " Please resend your message."
				if bs.recorder != nil {
					seq := bs.getNextSeq()
					if recErr := bs.recorder.RecordEventWithSeq(session.Event{
						Seq:       seq,
						Type:      session.EventTypeError,
						Timestamp: time.Now(),
						Data:      session.ErrorData{Message: friendlyMsg},
					}); recErr != nil && bs.logger != nil {
						bs.logger.Error("Failed to persist deferred handshake error", "error", recErr)
					}
					bs.refreshNextSeq()
				}
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(friendlyMsg)
				})
				bs.promptMu.Lock()
				bs.isPrompting = false
				bs.promptStartTime = time.Time{}
				bs.promptCond.Broadcast()
				bs.promptMu.Unlock()
				if bs.onStreamingStateChanged != nil {
					bs.onStreamingStateChanged(bs.persistedID, false)
				}
				return
			}
		}

		// For fresh-context runs, create a new ACP session so the agent has no
		// in-memory context from prior interactions. Only supported on non-shared
		// connections; shared-process sessions fall back to history suppression only.
		freshContextSessionID := ""
		if meta.FreshContext && bs.acpConn != nil {
			cwd := bs.workingDir
			if cwd == "" {
				cwd = "."
			}
			freshCtx, freshCancel := context.WithTimeout(bs.ctx, 10*time.Second)
			freshSess, freshErr := bs.acpConn.NewSession(freshCtx, acp.NewSessionRequest{
				Cwd:        cwd,
				McpServers: []acp.McpServer{}, // Must be empty array, not nil — ACP validates this
			})
			freshCancel()
			if freshErr == nil {
				freshContextSessionID = string(freshSess.SessionId)
				if bs.logger != nil {
					bs.logger.Info("Created fresh ACP session for periodic run",
						"fresh_session_id", freshContextSessionID,
						"session_id", bs.persistedID)
				}
			} else if bs.logger != nil {
				bs.logger.Warn("Failed to create fresh ACP session, using existing",
					"error", freshErr,
					"session_id", bs.persistedID)
			}
		}

		// Per-prompt model preference: ensure the correct model is active before sending.
		// Implements set-if-different: only one SetSessionModel call per model change,
		// never per-prompt (lazy). No-match and absent preferredModels both resolve to
		// baseline so a prior override is always cleared when not reused.
		if bs.agentModels != nil {
			preferredModels := meta.PreferredModels
			if len(preferredModels) == 0 && meta.PromptName != "" && bs.preferredModelsResolver != nil {
				preferredModels = bs.preferredModelsResolver(meta.PromptName, bs.workingDir)
			}

			bs.modelMu.Lock()
			baseline := bs.baselineModel
			bs.modelMu.Unlock()

			currentModel := string(bs.agentModels.CurrentModelId)
			desired := baseline // default: use user's baseline
			if len(preferredModels) > 0 {
				// Walk preferences in order, checking the active model first at each pattern
				// so a model that already satisfies a preference is kept (no needless switch).
				if resolved := SelectPreferredModel(preferredModels, bs.agentModels); resolved != "" {
					desired = resolved
				}
				// no match → desired stays as baseline (prevents override leakage)
			}

			// An override is in effect whenever the model we will run with differs from the
			// user's baseline; that's what restore-on-idle keys off.
			isOverride := desired != "" && desired != baseline
			if desired != "" && desired != currentModel {
				setCtx, setCancel := context.WithTimeout(bs.ctx, 15*time.Second)
				if setErr := bs.setActiveModelOnly(setCtx, desired); setErr != nil && bs.logger != nil {
					bs.logger.Warn("Failed to apply model preference",
						"model", desired, "error", setErr)
				}
				setCancel()
			}

			bs.modelMu.Lock()
			bs.overrideActive = isOverride
			bs.modelMu.Unlock()
		}

		// Declare all variables that are live across the retryPrompt goto target
		// here, before the label, so that Go's "no jumping over declarations" rule
		// is satisfied. They are assigned (not declared) inside the loop body.
		var (
			promptCtx       context.Context
			promptCancel    context.CancelFunc
			promptResp      acp.PromptResponse
			err             error
			promptStartedAt time.Time
			promptEndedAt   time.Time
			processDoneCh   <-chan struct{}
			connDoneCh      <-chan struct{}
			// inactivityWatchdogFired is set by the prompt inactivity watchdog when it
			// cancels the prompt because the agent stopped streaming (live-but-unresponsive).
			// The error-handling path below reads it to surface a recoverable message and
			// skip the crash-restart logic (the process is alive, not dead).
			inactivityWatchdogFired atomic.Bool
		)

	retryPrompt:
		// Reset the inactivity flag for this attempt (a goto retryPrompt reuses it).
		inactivityWatchdogFired.Store(false)
		// Create a prompt context that gets cancelled when the ACP process dies.
		// This ensures we fail fast instead of waiting for the ACP server's internal
		// 60-second control request timeout when the CLI subprocess has crashed.
		// See: claude-code-agent-sdk DEFAULT_CONTROL_REQUEST_TIMEOUT (60s)
		promptCtx, promptCancel = context.WithCancel(bs.ctx)
		// NOTE: no defer — we call promptCancel() explicitly after the prompt
		// returns so that (a) we clean up the health-monitor goroutine eagerly,
		// and (b) a goto back to retryPrompt doesn't accumulate extra defers.

		// Monitor ACP process health: if the connection's Done() channel closes
		// or the OS process exits (acpProcessDone), cancel the prompt context immediately.
		// The acpProcessDone channel provides faster detection than Done() because it
		// uses OS-level process liveness checks (signal 0) rather than waiting for
		// pipe EOF to propagate through the JSON-RPC transport layer.
		processDoneCh = bs.acpProcessDone // refresh on each retry (new process after restart)
		connDoneCh = nil                  // reset before assigning below
		if bs.acpConn != nil {
			connDoneCh = bs.acpConn.Done()
		} else if bs.sharedProcess != nil {
			connDoneCh = bs.sharedProcess.Done()
		}
		if connDoneCh != nil {
			go func() {
				select {
				case <-connDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP connection closed during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-processDoneCh:
					if bs.logger != nil {
						bs.logger.Warn("ACP process exited during prompt, cancelling",
							"session_id", bs.persistedID)
					}
					promptCancel()
				case <-promptCtx.Done():
					// Prompt completed normally or was cancelled for another reason
				}
			}()
		}

		// Monitor for a live-but-unresponsive agent: if the agent stops streaming any
		// updates for the configured window (and is not blocked on a UI prompt), cancel
		// the prompt so is_prompting clears and the user can resend. This catches the
		// "stuck, still responding" state that the process-death/connection monitors miss.
		bs.startPromptInactivityWatchdog(promptCtx, promptCancel, &inactivityWatchdogFired)

		// On retry after ACP crash, freshContextSessionID is from the old (dead)
		// connection; fall back to bs.acpID which holds the new session.
		acpSessionIDForPrompt := bs.acpID
		if freshContextSessionID != "" && !autoRetried {
			acpSessionIDForPrompt = freshContextSessionID
		}

		promptStartedAt = time.Now() // captured for after-phase processors
		if bs.sharedProcess != nil {
			promptResp, err = bs.sharedProcess.Prompt(promptCtx, acp.SessionId(acpSessionIDForPrompt), finalBlocks)
		} else {
			promptResp, err = bs.acpConn.Prompt(promptCtx, acp.PromptRequest{
				SessionId: acp.SessionId(acpSessionIDForPrompt),
				Prompt:    finalBlocks,
			})
		}
		promptCancel()             // cancel context to unblock the health-monitor goroutine
		promptEndedAt = time.Now() // captured for after-phase processors

		// Store token usage from the prompt response (if available).
		if promptResp.Usage != nil {
			bs.lastUsageMu.Lock()
			bs.lastUsage = promptResp.Usage
			bs.lastUsageMu.Unlock()
		}

		// Accumulate token usage for processor rerun tracking.
		if bs.processorManager != nil {
			if promptResp.Usage != nil {
				bs.processorManager.AccumulateTokenUsage(promptResp.Usage.TotalTokens)
			} else {
				// Fallback: estimate tokens from message text when ACP doesn't report usage.
				estimated := processors.EstimateTokens(message)
				// Also estimate from the agent's response if available.
				if bs.store != nil {
					if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
						agentMsg := session.GetLastAgentMessage(events)
						estimated += processors.EstimateTokens(agentMsg)
					}
				}
				if estimated > 0 {
					bs.processorManager.AccumulateTokenUsage(estimated)
				}
			}
		}

		// Mark prompt as complete BEFORE any further processing
		// This must happen before processNextQueuedMessage so the next message can be sent
		bs.promptMu.Lock()
		bs.isPrompting = false
		bs.promptStartTime = time.Time{}
		bs.lastResponseComplete = time.Now()
		bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
		bs.promptMu.Unlock()

		// Notify about streaming state change (prompt completed)
		if bs.onStreamingStateChanged != nil {
			bs.onStreamingStateChanged(bs.persistedID, false)
		}

		if bs.IsClosed() {
			return
		}

		// DEBUG: Log prompt completion sequence
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_sequence_start",
				"session_id", bs.persistedID,
				"observer_count", bs.ObserverCount(),
				"is_prompting", bs.IsPrompting())
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_start",
					"session_id", bs.persistedID)
			}
			bs.acpClient.FlushMarkdown()
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_done",
					"session_id", bs.persistedID)
			}
		}

		// Notify all observers
		eventCount := bs.GetEventCount()
		observerCount := bs.ObserverCount()
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_notify_start",
				"session_id", bs.persistedID,
				"event_count", eventCount,
				"observer_count", observerCount)
		}

		// sessionIdle becomes true only on the success path when the turn ended and
		// no further queued message was dispatched. It gates the on-completion periodic
		// idle hook invoked after OnComplete below.
		sessionIdle := false

		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("prompt_failed",
					"session_id", bs.persistedID,
					"error", err.Error(),
					"observer_count", observerCount)
			}

			// Check if the ACP process died (connection closed or OS process exited).
			// If so, attempt automatic restart rather than just showing an error.
			// We check both acpConn.Done() (JSON-RPC layer) and acpProcessDone
			// (OS-level process liveness) for faster detection.
			acpDead := false
			if bs.acpConn != nil {
				select {
				case <-bs.acpConn.Done():
					acpDead = true
				default:
				}
			} else if bs.sharedProcess != nil {
				select {
				case <-bs.sharedProcess.Done():
					acpDead = true
				default:
				}
			}
			if !acpDead && bs.acpProcessDone != nil {
				select {
				case <-bs.acpProcessDone:
					acpDead = true
				default:
				}
			}

			if inactivityWatchdogFired.Load() {
				// The agent stayed alive and connected but stopped streaming updates.
				// The watchdog already cancelled the prompt and is_prompting was cleared
				// above. Surface a recoverable message and do NOT auto-restart (the
				// process is healthy, not crashed) or auto-advance the queue (the next
				// queued message would likely wedge the same way).
				if bs.logger != nil {
					bs.logger.Warn("prompt_cancelled_by_inactivity_watchdog",
						"session_id", bs.persistedID)
				}
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("The AI agent stopped responding (no activity for a while), so the conversation was reset. Please resend your message. If this keeps happening, switch to another conversation and back to restart the agent.")
				})
			} else if acpDead && autoRetried {
				// The auto-retry already happened and the process crashed again.
				// Don't consume another restart slot — let the next user-triggered prompt
				// handle the restart. This ensures each user message uses at most one
				// restart slot, so MaxACPRestarts behaves predictably from the user's POV.
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("AI agent restarted. Please resend your message.")
				})
			} else if acpDead && bs.canRestartACP() {
				// First crash on this prompt — restart and automatically retry.
				restartInfo := bs.getRestartInfo()
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
				})
				if restartErr := bs.restartACPProcess(RestartReasonCrashDuringStream); restartErr != nil {
					// Provide specific guidance for permanent errors
					errMsg := "Failed to restart the AI agent: " + restartErr.Error() +
						". Please switch to another conversation and back to retry."
					if classified, ok := restartErr.(*ACPClassifiedError); ok && !classified.IsRetryable() {
						errMsg = formatClassifiedError(classified)
					}
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError(errMsg)
					})
				} else {
					// Restart succeeded — automatically retry the prompt.
					autoRetried = true
					bs.notifyObservers(func(o SessionObserver) {
						o.OnError("AI agent restarted. Retrying your message automatically...")
					})
					if bs.logger != nil {
						bs.logger.Info("Auto-retrying prompt after ACP restart during stream",
							"session_id", bs.persistedID)
					}
					// Re-acquire the prompting state so the retry runs under the
					// same invariants as the original prompt call.
					bs.promptMu.Lock()
					bs.isPrompting = true
					bs.promptStartTime = time.Now()
					bs.promptMu.Unlock()
					if bs.onStreamingStateChanged != nil {
						bs.onStreamingStateChanged(bs.persistedID, true)
					}
					goto retryPrompt
				}
			} else if acpDead {
				// ACP process died but restart limit exceeded — tell user to manually restart
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
				})
			} else {
				userFriendlyErr := formatACPError(err)
				bs.notifyObservers(func(o SessionObserver) {
					o.OnError(userFriendlyErr)
				})

				// Advance the queue for transient errors where the ACP process is
				// still healthy.  Skip queue processing for errors that indicate a
				// hard capacity or rate limit — sending the next queued message
				// immediately would cause the same failure again, creating a cascade
				// that drains the queue while showing a stream of identical errors.
				//
				// Context-too-large (413): all queued messages will fail until the
				//   user starts a fresh conversation — stop the queue.
				// Rate-limit: the API will reject the next message too — stop the
				//   queue; the keepalive-driven TryProcessQueuedMessage will retry
				//   once the session becomes idle and the delay has elapsed.
				if !isContextTooLargeError(err) && !isRateLimitError(err) {
					// Apply any config changes deferred during this turn before
					// dispatching the next queued message.
					bs.flushPendingConfig()
					bs.processNextQueuedMessage()
				}
			}
		} else {
			if bs.logger != nil {
				bs.logger.Debug("prompt_complete",
					"session_id", bs.persistedID,
					"event_count", eventCount,
					"observer_count", observerCount,
					"stop_reason", promptResp.StopReason)
			}
			bs.notifyObservers(func(o SessionObserver) {
				o.OnPromptComplete(eventCount)
			})

			// Apply any config changes deferred during this turn before dispatching
			// the next queued message, so the queued prompt runs under the new config.
			bs.flushPendingConfig()

			// Process next queued message if queue processing is enabled.
			// dispatched is true when another queued turn was started (the session is
			// not yet idle); it gates agentIdle after-phase processors below.
			dispatched := bs.processNextQueuedMessage()
			sessionIdle = !dispatched

			// Retry title generation if session still has no title.
			// This catches failed initial attempts (e.g. context deadline exceeded)
			// and prompts that arrived via paths that don't trigger title generation
			// (queue, MCP send_prompt, periodic).
			bs.retryTitleGenerationIfNeeded(message)

			// Async follow-up analysis (non-blocking)
			// This runs after prompt_complete so the user sees the response immediately
			// Note: 'message' is captured from the outer function scope (the user's prompt)
			isEndTurn := promptResp.StopReason == acp.StopReasonEndTurn
			if bs.actionButtonsConfig.IsEnabled() && isEndTurn {
				// Get the agent message from stored events (events are persisted immediately)
				var agentMessage string
				if bs.store != nil {
					if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
						agentMessage = session.GetLastAgentMessage(events)
					}
				}
				if agentMessage != "" {
					// Skip follow-up analysis if there are queued messages that will be processed immediately
					// (no delay configured). The suggestions would be stale by the time they arrive.
					if bs.hasImmediateQueuedMessages() {
						bs.logger.Debug("follow-up analysis: skipped due to pending immediate queue messages")
					} else {
						go bs.analyzeFollowUpQuestions(message, agentMessage)
					}
				}
			}

			// Apply after-phase processors (agentResponded + agentIdle pipeline).
			// Runs after follow-up analysis so all event state is fully persisted.
			// This is synchronous — processors are fast (command execution with timeouts).
			// sessionIdle is true when no further queued message was dispatched, so
			// agentIdle processors fire only once the queue has drained.
			if bs.processorManager != nil {
				bs.applyAfterProcessors(bs.ctx, message, meta.SenderID,
					string(promptResp.StopReason), promptStartedAt, promptEndedAt, promptResp, !dispatched)
			}
		}

		// Invoke OnComplete callback if set.
		// Called after all observers have been notified and state is consistent,
		// so the caller can accurately track the final outcome (nil = success, non-nil = failure).
		if meta.OnComplete != nil {
			meta.OnComplete(err)
		}

		// Notify the on-completion periodic hook once the agent has stopped and the
		// session is fully idle. Fired after OnComplete so any iteration accounting
		// (RecordSent / auto-stop) is applied before the next run is armed.
		if sessionIdle && bs.onTurnIdle != nil {
			bs.onTurnIdle(bs.persistedID)
		}

		// Self-destruct: if the agent requested deletion of its own conversation
		// during this turn, delete it now that the turn has fully completed and
		// observers have seen the final response. Run asynchronously so this
		// goroutine can unwind before the session (and its ACP connection) is
		// torn down by the deletion path.
		if bs.IsSelfDestructRequested() && bs.onSelfDestruct != nil {
			if bs.logger != nil {
				bs.logger.Info("self_destruct_triggered", "session_id", bs.persistedID)
			}
			go bs.onSelfDestruct(bs.persistedID)
		}
	}()

	return nil
}

// Cancel cancels the current prompt and resets the prompting state.
// This sends a cancel notification to the ACP agent and resets the isPrompting flag
// so the session can accept new prompts even if the agent doesn't respond to the cancel.
func (bs *BackgroundSession) Cancel() error {
	// Dismiss any active UI prompt first (MCP tool questions, permissions, etc.)
	// This ensures the UI is cleaned up when the user presses Stop.
	bs.DismissActiveUIPrompt()

	// Reset prompting state regardless of whether cancel succeeds
	// This ensures the session can accept new prompts even if the agent is unresponsive
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	// Send cancel notification to ACP agent (best effort)
	var cancelErr error
	if bs.sharedProcess != nil {
		cancelErr = bs.sharedProcess.Cancel(bs.ctx, acp.SessionId(bs.acpID))
	} else if bs.acpConn != nil {
		cancelErr = bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
			SessionId: acp.SessionId(bs.acpID),
		})
	}

	// Apply any config changes deferred during the cancelled turn now that the
	// session is idle.
	if wasPrompting {
		bs.flushPendingConfig()
	}

	return cancelErr
}

// ForceReset forcefully resets the session's prompting state.
// This is used when the agent is completely unresponsive and Cancel doesn't work.
// It resets the isPrompting flag, flushes any buffered content, and notifies observers.
// Unlike Cancel, this does NOT send a cancel notification to the agent.
func (bs *BackgroundSession) ForceReset() {
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	if !wasPrompting {
		if bs.logger != nil {
			bs.logger.Debug("ForceReset called but session was not prompting")
		}
		return
	}

	// Flush any buffered content
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}

	// Notify observers that the prompt was forcefully reset
	eventCount := bs.GetEventCount()
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPromptComplete(eventCount)
	})

	// Apply any config changes deferred during the reset turn now that the session
	// is idle (best effort; the RPC fails fast if the agent connection is dead).
	bs.flushPendingConfig()

	if bs.logger != nil {
		bs.logger.Warn("Session forcefully reset due to unresponsive agent")
	}
}
