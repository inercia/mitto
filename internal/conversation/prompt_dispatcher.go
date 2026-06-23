package conversation

// PromptWithMeta helper-split collaborator — stateless; state lives on BackgroundSession.
// This collaborator holds extracted chunks of PromptWithMeta that are safe to split out
// (no goto, no goroutine). More chunks will be absorbed in later 2.5-c sub-increments.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// promptDeps is the minimal interface promptDispatcher needs from BackgroundSession.
// All methods are prefixed with "pd" to avoid clashing with BackgroundSession's public API.
type promptDeps interface {
	// Prompt resolver
	pdPromptResolver() PromptResolver // may return nil
	pdWorkingDir() string

	// Agent capabilities
	pdAgentSupportsImages() bool

	// Store access for attachment loading (nil-safe)
	pdHasStore() bool
	pdGetImagePath(imageID string) (string, error)
	pdGetFilePath(fileID string) (string, error)

	// Logging + observer fan-out
	pdLogger() *slog.Logger
	pdSessionID() string
	pdNotifyObservers(fn func(SessionObserver))

	// === New in 2.5-b: processor-input + apply-processors helpers ===

	// Workspace / session identity
	pdWorkspaceUUID() string
	pdAvailableACPServers() []processors.AvailableACPServer

	// Store — session metadata (guard: store must be available)
	pdGetSessionMetadata() (session.Metadata, error)
	pdGetMetadataForID(id string) (session.Metadata, error)
	pdListChildSessions() ([]session.Metadata, error)
	pdIsChildPrompting(childSessionID string) bool

	// MCP tool names from the auxiliary manager (empty when unavailable)
	pdCachedMCPToolNames() []string

	// User data from the store (nil when unavailable or empty)
	pdGetUserData() (*session.UserData, error)

	// Processor pipeline
	pdSessionCtx() context.Context
	pdHasProcessorManager() bool
	pdApplyProcessors(ctx context.Context, input *processors.ProcessorInput) (*processors.ProcessorResult, error)
	// pdPersistProcessorActivation persists the activation count to metadata after Apply.
	// No-op when no store or persistedID.
	pdPersistProcessorActivation()

	// History injection
	pdBuildPromptWithHistory(message string) string

	// === New in 2.5-c: goroutine-top setup helpers ===

	// Handshake
	pdHasSharedProcess() bool
	pdCompleteDeferredHandshake() error

	// Error event recording (for handshake failure)
	pdHasRecorder() bool
	pdGetNextSeq() int64
	pdRefreshNextSeq()
	pdRecordErrorEvent(seq int64, msg string) error

	// Prompting-state reset on handshake abort
	pdResetPromptingStateForAbort()            // promptMu + isPrompting=false + promptStartTime zero + Broadcast
	pdNotifyStreamingStateChanged(active bool) // no-op if hook not set

	// Fresh-context session creation
	pdHasACPConn() bool
	pdACPConnNewSession(ctx context.Context, cwd string) (string, error)

	// Per-prompt model preference
	pdGetAgentModels() *acp.UnstableSessionModelState // may return nil
	pdResolvePreferredModels(promptName string) []string
	pdReadBaselineModel() string       // modelMu.Lock + read + Unlock
	pdWriteOverrideActive(active bool) // modelMu.Lock + write + Unlock
	pdSetActiveModelOnly(ctx context.Context, modelID string) error

	// === New in 2.5-d: post-prompt completion helpers ===

	// Token usage bookkeeping
	pdSetLastUsage(usage *acp.Usage)            // lastUsageMu.Lock + lastUsage = usage + Unlock
	pdAccumulateTokenUsage(tokens int)          // processorManager.AccumulateTokenUsage
	pdEstimateTokensFromMessage(msg string) int // processors.EstimateTokens(msg)
	pdReadLastAgentMessage() string             // ReadEvents + GetLastAgentMessage; returns "" on any error

	// Streaming state completion (promptMu critical section)
	pdMarkPromptComplete() // promptMu: isPrompting=false, promptStartTime=time.Time{}, lastResponseComplete=time.Now(), Broadcast
	pdIsClosed() bool      // session closed check

	// Markdown flush (acpClient nil-safe)
	pdFlushMarkdown() // no-op when acpClient is nil

	// Observer counts
	pdObserverCount() int
	pdGetEventCount() int

	// Success-path processing
	pdFlushPendingConfig()                         // apply config changes deferred during the turn
	pdProcessNextQueuedMessage() bool              // returns true when a queued message was dispatched
	pdRetryTitleGenerationIfNeeded(message string) // re-trigger title gen if session has no title
	pdActionButtonsEnabled() bool                  // actionButtonsConfig.IsEnabled()
	pdReadLastAgentMessageFromStore() string       // same as pdReadLastAgentMessage (kept separate for clarity)
	pdHasImmediateQueuedMessages() bool
	pdStartFollowUpAnalysis(userMessage, agentMessage string) // go bs.analyzeFollowUpQuestions(...)
	pdApplyAfterProcessors(ctx context.Context, message, senderID, stopReason string,
		startedAt, endedAt time.Time, resp acp.PromptResponse, agentIdle bool)

	// Turn finalization
	pdOnTurnIdle() // no-op if not sessionIdle or hook not set
	pdIsSelfDestructRequested() bool
	pdTriggerSelfDestruct() // go bs.onSelfDestruct(bs.persistedID)

	// === New in 2.5-e: error-branch helpers ===

	// pdIsACPDead checks all three liveness sources (acpConn.Done, sharedProcess.Done,
	// acpProcessDone) with non-blocking selects. Returns true if any source is closed.
	pdIsACPDead() bool
	pdCanRestartACP() bool
	pdGetRestartInfo() string
	pdRestartACPProcess() error // bakes in RestartReasonCrashDuringStream
	pdReacquirePromptingState() // promptMu: isPrompting=true, promptStartTime=now, Unlock
}

// promptDispatcher is a stateless collaborator holding safe synchronous chunks of
// PromptWithMeta that contain no goto labels and no goroutines.
type promptDispatcher struct{}

// resolveAndSubstitute covers the top of PromptWithMeta (lines 165–201 in the original):
//  1. If meta.PromptName != "" && message == "": resolve the prompt name to full text
//     (error if no resolver, or if resolution fails).
//  2. Record argCount = len(meta.Arguments).
//  3. If argCount > 0: apply bash-like argument substitution to the message.
//  4. If argCount > 0: build argument metadata and annotate meta.Meta.
//
// Returns (resolvedMessage, argCount, updatedMeta, error). On non-nil error the
// caller should return the error immediately (the two early-return paths are preserved).
func (p promptDispatcher) resolveAndSubstitute(d promptDeps, message string, meta PromptMeta) (string, int, PromptMeta, error) {
	if meta.PromptName != "" && message == "" {
		resolver := d.pdPromptResolver()
		if resolver == nil {
			return "", 0, meta, &promptResolverError{name: meta.PromptName}
		}
		resolved, err := resolver(meta.PromptName, d.pdWorkingDir())
		if err != nil {
			return "", 0, meta, &promptResolutionError{name: meta.PromptName, cause: err}
		}
		message = resolved
	}

	argCount := len(meta.Arguments)

	if argCount > 0 {
		message = processors.SubstituteArguments(message, meta.Arguments)
	}

	if argCount > 0 {
		names, arguments := buildArgumentMetadata(meta.Arguments)
		if meta.Meta == nil {
			meta.Meta = make(map[string]any)
		}
		meta.Meta["argument_names"] = names
		meta.Meta["arguments"] = arguments
	}

	return message, argCount, meta, nil
}

// buildAttachmentBlocks covers the image+file loading section (lines 330–431):
//   - Warns (but still sends) when images are requested and the agent has no image support.
//   - Loads each image from disk via the store; skips on error (warn-and-continue).
//   - Loads each file; picks TextFileAttachment vs BinaryFileAttachment based on category.
//   - Returns content blocks (to prepend to the ACP prompt), imageRefs and fileRefs
//     (for session persistence).
func (p promptDispatcher) buildAttachmentBlocks(d promptDeps, imageIDs, fileIDs []string) (
	contentBlocks []acp.ContentBlock,
	imageRefs []session.ImageRef,
	fileRefs []session.FileRef,
) {
	if len(imageIDs) > 0 && !d.pdAgentSupportsImages() {
		if l := d.pdLogger(); l != nil {
			l.Warn("Agent did not advertise image support, sending images anyway",
				"image_count", len(imageIDs),
				"session_id", d.pdSessionID())
		}
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError("⚠️ The current AI agent did not advertise image support. " +
				"Images will be sent anyway, but may not be processed correctly.")
		})
	}

	if len(imageIDs) > 0 && d.pdHasStore() {
		for _, imageID := range imageIDs {
			imagePath, err := d.pdGetImagePath(imageID)
			if err != nil {
				if l := d.pdLogger(); l != nil {
					l.Warn("Failed to get image path", "image_id", imageID, "error", err)
				}
				continue
			}

			ext := ""
			if idx := strings.LastIndex(imageID, "."); idx >= 0 {
				ext = imageID[idx:]
			}
			mimeType := session.GetMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "image/png"
			}

			att, err := mittoAcp.ImageAttachmentFromFile(imagePath, mimeType)
			if err != nil {
				if l := d.pdLogger(); l != nil {
					l.Warn("Failed to load image", "image_id", imageID, "error", err)
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

	if len(fileIDs) > 0 && d.pdHasStore() {
		for _, fileID := range fileIDs {
			filePath, err := d.pdGetFilePath(fileID)
			if err != nil {
				if l := d.pdLogger(); l != nil {
					l.Warn("Failed to get file path", "file_id", fileID, "error", err)
				}
				continue
			}

			ext := ""
			if idx := strings.LastIndex(fileID, "."); idx >= 0 {
				ext = fileID[idx:]
			}
			mimeType := session.GetFileMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			category := session.GetFileCategory(mimeType)
			var att mittoAcp.Attachment
			if category == session.FileCategoryText {
				att, err = mittoAcp.TextFileAttachmentFromFile(filePath, mimeType)
				if err != nil {
					if l := d.pdLogger(); l != nil {
						l.Warn("Failed to load text file", "file_id", fileID, "error", err)
					}
					continue
				}
			} else {
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

	return contentBlocks, imageRefs, fileRefs
}

// buildProcessorInput assembles the *processors.ProcessorInput for PromptWithMeta
// (current lines ~364–467 before extraction). Covers: session-metadata fetch,
// parent-name resolution, child-session list, MCP tool names, user-data-schema /
// .mittorc / user-data population, and final struct assembly.
// All fetches are best-effort (errors are swallowed; missing fields become "").
func (p promptDispatcher) buildProcessorInput(d promptDeps, message string, isFirst bool, meta PromptMeta) *processors.ProcessorInput {
	var sessionName, acpServer, parentSessionID, parentSessionName, beadsIssue string
	var childSessions []processors.ChildSession
	var advancedSettings map[string]bool

	if d.pdHasStore() {
		if sessionMeta, err := d.pdGetSessionMetadata(); err == nil {
			sessionName = sessionMeta.Name
			acpServer = sessionMeta.ACPServer
			parentSessionID = sessionMeta.ParentSessionID
			advancedSettings = sessionMeta.AdvancedSettings
			beadsIssue = sessionMeta.BeadsIssue
		}
		if parentSessionID != "" {
			if parentMeta, err := d.pdGetMetadataForID(parentSessionID); err == nil {
				parentSessionName = parentMeta.Name
			}
		}
		if children, err := d.pdListChildSessions(); err == nil {
			for _, child := range children {
				isPrompting := d.pdIsChildPrompting(child.SessionID)
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

	mcpToolNames := d.pdCachedMCPToolNames()

	var hasUserDataSchema bool
	var hasMittoRC bool
	var hasMetadataDescription bool
	var userDataSchemaJSON string
	workingDir := d.pdWorkingDir()
	if workingDir != "" {
		rc, rcErr := config.LoadWorkspaceRC(workingDir)
		if rcErr == nil && rc != nil &&
			rc.Metadata != nil && rc.Metadata.UserDataSchema != nil && len(rc.Metadata.UserDataSchema.Fields) > 0 {
			hasUserDataSchema = true
			if schemaBytes, err := json.Marshal(rc.Metadata.UserDataSchema.Fields); err == nil {
				userDataSchemaJSON = string(schemaBytes)
			}
		}
		if rcPath, _, err := config.FindWorkspaceRCPath(workingDir); err == nil && rcPath != "" {
			hasMittoRC = true
		}
		if rcErr == nil && rc != nil && rc.Metadata != nil && rc.Metadata.Description != "" {
			hasMetadataDescription = true
		}
	}

	var userDataJSON string
	if d.pdHasStore() {
		if ud, err := d.pdGetUserData(); err == nil && ud != nil && len(ud.Attributes) > 0 {
			if udBytes, err := json.Marshal(ud.Attributes); err == nil {
				userDataJSON = string(udBytes)
			}
		}
	}

	return &processors.ProcessorInput{
		Message:                message,
		IsFirstMessage:         isFirst,
		SessionID:              d.pdSessionID(),
		WorkingDir:             workingDir,
		ParentSessionID:        parentSessionID,
		ParentSessionName:      parentSessionName,
		SessionName:            sessionName,
		ACPServer:              acpServer,
		WorkspaceUUID:          d.pdWorkspaceUUID(),
		BeadsIssue:             beadsIssue,
		AvailableACPServers:    d.pdAvailableACPServers(),
		ChildSessions:          childSessions,
		MCPToolNames:           mcpToolNames,
		IsPeriodic:             meta.SenderID == "periodic-runner",
		IsPeriodicForced:       meta.IsPeriodicForced,
		Arguments:              meta.Arguments,
		AdvancedSettings:       advancedSettings,
		HasUserDataSchema:      hasUserDataSchema,
		HasMittoRC:             hasMittoRC,
		HasMetadataDescription: hasMetadataDescription,
		UserDataSchemaJSON:     userDataSchemaJSON,
		UserDataJSON:           userDataJSON,
	}
}

// applyProcessorsAndBuildBlocks covers lines ~469–543 of the original PromptWithMeta:
// runs the processor pipeline, persists activation metadata, converts attachments to
// image content blocks, applies @mitto:variable substitution, optionally injects history,
// and assembles finalBlocks in the canonical order (uploads → proc-attachments → text).
func (p promptDispatcher) applyProcessorsAndBuildBlocks(
	d promptDeps,
	input *processors.ProcessorInput,
	message string,
	contentBlocks []acp.ContentBlock,
	shouldInjectHistory bool,
) []acp.ContentBlock {
	promptMessage := message
	var procAttachmentBlocks []acp.ContentBlock

	if d.pdHasProcessorManager() {
		procResult, procErr := d.pdApplyProcessors(d.pdSessionCtx(), input)
		if procErr != nil {
			if l := d.pdLogger(); l != nil {
				l.Error("Processor execution failed", "error", procErr)
			}
			// Continue with original message on processor failure.
		} else {
			d.pdPersistProcessorActivation()
		}
		if procResult != nil {
			promptMessage = procResult.Message
			if len(procResult.Attachments) > 0 {
				acpAttachments, err := procResult.ToACPAttachments(d.pdWorkingDir())
				if err != nil {
					if l := d.pdLogger(); l != nil {
						l.Error("Failed to resolve processor attachments", "error", err)
					}
				} else {
					for _, att := range acpAttachments {
						if att.Type == "image" {
							procAttachmentBlocks = append(procAttachmentBlocks, acp.ImageBlock(att.Data, att.MimeType))
						}
					}
				}
			}
		}
	}

	promptMessage = processors.SubstituteVariables(promptMessage, input)

	if shouldInjectHistory {
		promptMessage = d.pdBuildPromptWithHistory(promptMessage)
	}

	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+len(procAttachmentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, procAttachmentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	if l := d.pdLogger(); l != nil {
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
		l.Info("Sending prompt to ACP agent",
			"total_blocks", len(finalBlocks),
			"image_blocks", imageBlockCount,
			"text_blocks", textBlockCount,
			"other_blocks", otherBlockCount,
			"processor_attachment_blocks", len(procAttachmentBlocks),
			"agent_supports_images", d.pdAgentSupportsImages(),
			"session_id", d.pdSessionID())
	}

	return finalBlocks
}

// completeHandshakeOrAbort handles the deferred session/new handshake for shared-process
// sessions at the top of the PromptWithMeta goroutine. Returns true to continue, false to
// abort (caller must return from the goroutine). When no shared process is configured it
// is always a no-op that returns true.
func (p promptDispatcher) completeHandshakeOrAbort(d promptDeps) bool {
	if !d.pdHasSharedProcess() {
		return true
	}

	const maxHandshakeAttempts = 3
	var handshakeErr error
	for attempt := 1; attempt <= maxHandshakeAttempts; attempt++ {
		handshakeErr = d.pdCompleteDeferredHandshake()
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
		if l := d.pdLogger(); l != nil {
			l.Warn("Deferred session/new transient failure, retrying",
				"session_id", d.pdSessionID(),
				"attempt", attempt,
				"error", handshakeErr)
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}

	if handshakeErr == nil {
		return true
	}

	if l := d.pdLogger(); l != nil {
		l.Error("Deferred session/new failed",
			"session_id", d.pdSessionID(),
			"error", handshakeErr)
	}
	friendlyMsg := "Could not start the agent session: " + formatACPError(handshakeErr) + " Please resend your message."
	if d.pdHasRecorder() {
		seq := d.pdGetNextSeq()
		if recErr := d.pdRecordErrorEvent(seq, friendlyMsg); recErr != nil {
			if l := d.pdLogger(); l != nil {
				l.Error("Failed to persist deferred handshake error", "error", recErr)
			}
		}
		d.pdRefreshNextSeq()
	}
	d.pdNotifyObservers(func(o SessionObserver) { o.OnError(friendlyMsg) })
	d.pdResetPromptingStateForAbort()
	d.pdNotifyStreamingStateChanged(false)
	return false
}

// createFreshContextSession creates a new ACP session for fresh-context runs.
// Returns the new session ID, or "" if FreshContext is not requested or the
// connection is unavailable.
func (p promptDispatcher) createFreshContextSession(d promptDeps, meta PromptMeta) string {
	if !meta.FreshContext || !d.pdHasACPConn() {
		return ""
	}
	cwd := d.pdWorkingDir()
	if cwd == "" {
		cwd = "."
	}
	freshCtx, freshCancel := context.WithTimeout(d.pdSessionCtx(), 10*time.Second)
	sessID, err := d.pdACPConnNewSession(freshCtx, cwd)
	freshCancel()
	if err == nil {
		if l := d.pdLogger(); l != nil {
			l.Info("Created fresh ACP session for periodic run",
				"fresh_session_id", sessID,
				"session_id", d.pdSessionID())
		}
		return sessID
	}
	if l := d.pdLogger(); l != nil {
		l.Warn("Failed to create fresh ACP session, using existing",
			"error", err,
			"session_id", d.pdSessionID())
	}
	return ""
}

// applyModelPreference ensures the correct model is active before sending the prompt.
// Implements set-if-different (lazy): only issues a SetSessionModel RPC when the
// desired model differs from the current active model. No-op when agentModels is nil.
func (p promptDispatcher) applyModelPreference(d promptDeps, meta PromptMeta) {
	models := d.pdGetAgentModels()
	if models == nil {
		return
	}

	preferredModels := meta.PreferredModels
	if len(preferredModels) == 0 && meta.PromptName != "" {
		preferredModels = d.pdResolvePreferredModels(meta.PromptName)
	}

	baseline := d.pdReadBaselineModel()
	currentModel := string(models.CurrentModelId)
	desired := baseline
	if len(preferredModels) > 0 {
		if resolved := SelectPreferredModel(preferredModels, models); resolved != "" {
			desired = resolved
		}
		// no match → desired stays as baseline (prevents override leakage)
	}

	isOverride := desired != "" && desired != baseline
	if desired != "" && desired != currentModel {
		setCtx, setCancel := context.WithTimeout(d.pdSessionCtx(), 15*time.Second)
		if setErr := d.pdSetActiveModelOnly(setCtx, desired); setErr != nil {
			if l := d.pdLogger(); l != nil {
				l.Warn("Failed to apply model preference", "model", desired, "error", setErr)
			}
		}
		setCancel()
	}

	d.pdWriteOverrideActive(isOverride)
}

// accumulateTokenUsage stores and accumulates token usage from a prompt response.
// When the response includes usage, it stores it and accumulates the total tokens.
// When usage is absent, it falls back to text-based estimation from the message
// and the last agent response.
func (p promptDispatcher) accumulateTokenUsage(d promptDeps, promptResp acp.PromptResponse, message string) {
	if promptResp.Usage != nil {
		d.pdSetLastUsage(promptResp.Usage)
	}

	if !d.pdHasProcessorManager() {
		return
	}

	if promptResp.Usage != nil {
		d.pdAccumulateTokenUsage(promptResp.Usage.TotalTokens)
	} else {
		// Fallback: estimate tokens from message text when ACP doesn't report usage.
		estimated := d.pdEstimateTokensFromMessage(message)
		// Also estimate from the agent's response if available.
		agentMsg := d.pdReadLastAgentMessage()
		estimated += d.pdEstimateTokensFromMessage(agentMsg)
		if estimated > 0 {
			d.pdAccumulateTokenUsage(estimated)
		}
	}
}

// markPromptCompleteAndFlush resets the prompting state, notifies streaming observers,
// checks for session closure, logs the completion sequence, and flushes the markdown buffer.
// Returns true if the session is closed (caller must return immediately); false otherwise.
func (p promptDispatcher) markPromptCompleteAndFlush(d promptDeps) (closed bool) {
	// Mark prompt as complete BEFORE any further processing.
	// This must happen before processNextQueuedMessage so the next message can be sent.
	d.pdMarkPromptComplete()

	// Notify about streaming state change (prompt completed).
	d.pdNotifyStreamingStateChanged(false)

	if d.pdIsClosed() {
		return true
	}

	// DEBUG: Log prompt completion sequence.
	if l := d.pdLogger(); l != nil {
		l.Debug("prompt_completion_sequence_start",
			"session_id", d.pdSessionID(),
			"observer_count", d.pdObserverCount(),
			"is_prompting", false)
	}

	// Flush markdown buffer.
	if l := d.pdLogger(); l != nil {
		l.Debug("prompt_completion_flush_markdown_start",
			"session_id", d.pdSessionID())
	}
	d.pdFlushMarkdown()
	if l := d.pdLogger(); l != nil {
		l.Debug("prompt_completion_flush_markdown_done",
			"session_id", d.pdSessionID())
	}

	return false
}

// handlePromptSuccess handles the success path after a prompt completes without error.
// It notifies observers, flushes pending config, dispatches the next queued message,
// retries title generation, triggers follow-up analysis when appropriate, and applies
// after-phase processors. Returns true when the session becomes idle (no queued message
// was dispatched).
func (p promptDispatcher) handlePromptSuccess(
	d promptDeps,
	eventCount, observerCount int,
	promptResp acp.PromptResponse,
	message string,
	meta PromptMeta,
	promptStartedAt, promptEndedAt time.Time,
) (sessionIdle bool) {
	if l := d.pdLogger(); l != nil {
		l.Debug("prompt_complete",
			"session_id", d.pdSessionID(),
			"event_count", eventCount,
			"observer_count", observerCount,
			"stop_reason", promptResp.StopReason)
	}
	d.pdNotifyObservers(func(o SessionObserver) {
		o.OnPromptComplete(eventCount)
	})

	// Apply any config changes deferred during this turn before dispatching
	// the next queued message, so the queued prompt runs under the new config.
	d.pdFlushPendingConfig()

	// Process next queued message if queue processing is enabled.
	// dispatched is true when another queued turn was started (the session is
	// not yet idle); it gates agentIdle after-phase processors below.
	dispatched := d.pdProcessNextQueuedMessage()
	sessionIdle = !dispatched

	// Retry title generation if session still has no title.
	d.pdRetryTitleGenerationIfNeeded(message)

	// Async follow-up analysis (non-blocking).
	isEndTurn := promptResp.StopReason == acp.StopReasonEndTurn
	if d.pdActionButtonsEnabled() && isEndTurn {
		agentMessage := d.pdReadLastAgentMessageFromStore()
		if agentMessage != "" {
			if d.pdHasImmediateQueuedMessages() {
				if l := d.pdLogger(); l != nil {
					l.Debug("follow-up analysis: skipped due to pending immediate queue messages")
				}
			} else {
				d.pdStartFollowUpAnalysis(message, agentMessage)
			}
		}
	}

	// Apply after-phase processors (agentResponded + agentIdle pipeline).
	d.pdApplyAfterProcessors(d.pdSessionCtx(), message, meta.SenderID,
		string(promptResp.StopReason), promptStartedAt, promptEndedAt, promptResp, !dispatched)

	return sessionIdle
}

// finalizeTurn invokes the OnComplete callback, the on-turn-idle hook, and
// self-destruct (in that order). It is called after both the success and error
// paths have been processed. The order is intentional: OnComplete fires first so
// any iteration accounting is applied before idle hooks and self-destruct.
func (p promptDispatcher) finalizeTurn(d promptDeps, err error, meta PromptMeta, sessionIdle bool) {
	// Invoke OnComplete callback if set.
	if meta.OnComplete != nil {
		meta.OnComplete(err)
	}

	// Notify the on-completion periodic hook once the agent has stopped and the
	// session is fully idle.
	if sessionIdle {
		d.pdOnTurnIdle()
	}

	// Self-destruct: if the agent requested deletion of its own conversation during
	// this turn, delete it now that the turn has fully completed and observers have
	// seen the final response.
	if d.pdIsSelfDestructRequested() {
		if l := d.pdLogger(); l != nil {
			l.Info("self_destruct_triggered", "session_id", d.pdSessionID())
		}
		d.pdTriggerSelfDestruct()
	}
}

// handlePromptError handles the error branch of PromptWithMeta's retry loop.
// It inspects the error, detects ACP process death, and takes the appropriate action:
//   - inactivity watchdog fired → surface recoverable message, return false
//   - ACP dead + already auto-retried → surface "resend" message, return false
//   - ACP dead + can restart → restart, notify, reacquire prompting state, return true (caller gotos retryPrompt)
//   - ACP dead + restart fails → surface failure message, return false
//   - ACP dead + restart limit exceeded → surface crash message, return false
//   - transient error (process alive) → surface ACP error, conditionally advance queue, return false
//
// autoRetried is a pointer because the restart-success path sets it to true, and the
// updated value must persist in the goroutine across the goto retryPrompt back-edge.
func (p promptDispatcher) handlePromptError(
	d promptDeps,
	err error,
	autoRetried *bool,
	observerCount int,
	inactivityWatchdogFired bool,
) (retry bool) {
	if l := d.pdLogger(); l != nil {
		l.Error("prompt_failed",
			"session_id", d.pdSessionID(),
			"error", err.Error(),
			"observer_count", observerCount)
	}

	acpDead := d.pdIsACPDead()

	if inactivityWatchdogFired {
		// The agent stayed alive and connected but stopped streaming updates.
		// The watchdog already cancelled the prompt and is_prompting was cleared above.
		// Surface a recoverable message and do NOT auto-restart (the process is healthy,
		// not crashed) or auto-advance the queue (the next queued message would likely
		// wedge the same way).
		if l := d.pdLogger(); l != nil {
			l.Warn("prompt_cancelled_by_inactivity_watchdog",
				"session_id", d.pdSessionID())
		}
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError("The AI agent stopped responding (no activity for a while), so the conversation was reset. Please resend your message. If this keeps happening, switch to another conversation and back to restart the agent.")
		})
		return false
	} else if acpDead && *autoRetried {
		// The auto-retry already happened and the process crashed again.
		// Don't consume another restart slot — let the next user-triggered prompt
		// handle the restart. This ensures each user message uses at most one
		// restart slot, so MaxACPRestarts behaves predictably from the user's POV.
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError("AI agent restarted. Please resend your message.")
		})
		return false
	} else if acpDead && d.pdCanRestartACP() {
		// First crash on this prompt — restart and automatically retry.
		restartInfo := d.pdGetRestartInfo()
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError(fmt.Sprintf("The AI agent process stopped unexpectedly. Restarting %s...", restartInfo))
		})
		if restartErr := d.pdRestartACPProcess(); restartErr != nil {
			// Provide specific guidance for permanent errors.
			errMsg := "Failed to restart the AI agent: " + restartErr.Error() +
				". Please switch to another conversation and back to retry."
			if classified, ok := restartErr.(*ACPClassifiedError); ok && !classified.IsRetryable() {
				errMsg = formatClassifiedError(classified)
			}
			d.pdNotifyObservers(func(o SessionObserver) {
				o.OnError(errMsg)
			})
			return false
		}
		// Restart succeeded — automatically retry the prompt.
		*autoRetried = true
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError("AI agent restarted. Retrying your message automatically...")
		})
		if l := d.pdLogger(); l != nil {
			l.Info("Auto-retrying prompt after ACP restart during stream",
				"session_id", d.pdSessionID())
		}
		// Re-acquire the prompting state so the retry runs under the
		// same invariants as the original prompt call.
		d.pdReacquirePromptingState()
		d.pdNotifyStreamingStateChanged(true)
		return true
	} else if acpDead {
		// ACP process died but restart limit exceeded — tell user to manually restart.
		d.pdNotifyObservers(func(o SessionObserver) {
			o.OnError("The AI agent keeps crashing. Please switch to another conversation and back to restart.")
		})
		return false
	}

	// Transient error: ACP process is still alive.
	userFriendlyErr := formatACPError(err)
	d.pdNotifyObservers(func(o SessionObserver) {
		o.OnError(userFriendlyErr)
	})

	// Advance the queue for transient errors where the ACP process is still healthy.
	// Skip queue processing for errors that indicate a hard capacity or rate limit —
	// sending the next queued message immediately would cause the same failure again,
	// creating a cascade that drains the queue while showing a stream of identical errors.
	//
	// Context-too-large (413): all queued messages will fail until the user starts a fresh
	//   conversation — stop the queue.
	// Rate-limit: the API will reject the next message too — stop the queue;
	//   the keepalive-driven TryProcessQueuedMessage will retry once the session is idle.
	if !isContextTooLargeError(err) && !isRateLimitError(err) {
		// Apply any config changes deferred during this turn before
		// dispatching the next queued message.
		d.pdFlushPendingConfig()
		d.pdProcessNextQueuedMessage()
	}
	return false
}

// promptResolverError is returned when no resolver is configured.
type promptResolverError struct{ name string }

func (e *promptResolverError) Error() string {
	return "prompt " + strQuote(e.name) + " cannot be resolved: no prompt resolver configured"
}

// promptResolutionError wraps resolver errors.
type promptResolutionError struct {
	name  string
	cause error
}

func (e *promptResolutionError) Error() string {
	return "failed to resolve prompt " + strQuote(e.name) + ": " + e.cause.Error()
}

func (e *promptResolutionError) Unwrap() error { return e.cause }

// strQuote returns name surrounded by double quotes (avoids importing fmt).
func strQuote(s string) string { return `"` + s + `"` }
