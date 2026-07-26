package conversation

// PromptWithMeta helper-split collaborator — stateless; state lives on BackgroundSession.
// This collaborator holds extracted chunks of PromptWithMeta that are safe to split out
// (no goto, no goroutine). More chunks will be absorbed in later 2.5-c sub-increments.

import (
	"context"
	"encoding/json"
	"errors"
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
	// pdWorkspaceProcessorArgOverrides returns the per-workspace processor argument overrides
	// from the folder's .mittorc (procName → argName → value). Used to populate
	// ProcessorInput.ProcessorArgOverrides for Go-template .Args in prompt-mode processors.
	pdWorkspaceProcessorArgOverrides() map[string]map[string]string
	// pdPersistProcessorActivation persists the activation count to metadata after Apply.
	// No-op when no store or persistedID.
	pdPersistProcessorActivation()

	// History injection
	pdBuildPromptWithHistory(message string) string

	// === New in 2.5-c: goroutine-top setup helpers ===

	// Handshake
	pdHasSharedProcess() bool
	pdCompleteDeferredHandshake() error
	// pdRecommendedHandshakeDeadline returns the outer wall-clock budget the
	// deferred session/new handshake should be bounded by (mitto-f51). Derived
	// from the shared process's own RecommendedLoadTimeout so we do not truncate
	// a legitimate cold handshake. Returns 0 to indicate the caller should apply
	// its own default.
	pdRecommendedHandshakeDeadline() time.Duration

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
	pdGetAgentModels() *SessionModelState         // may return nil
	pdResolveModelTags(modelName string) []string // config.ResolveModelTags; nil when no config/match
	pdResolvePreferredModels(promptName string) []config.PromptPreferredModel
	pdModelProfiles() []config.ModelProfile // global model profiles (Settings → Models)
	pdReadBaselineModel() string            // modelMu.Lock + read + Unlock
	pdWriteOverrideActive(active bool)      // modelMu.Lock + write + Unlock
	pdSetActiveModelOnly(ctx context.Context, modelID string) error
	// pdRecordSessionChange assigns a seq, persists a session-change timeline
	// event via the recorder, and notifies observers. Used for the model-override pill.
	pdRecordSessionChange(kind, value, previousValue string)
	// pdRecordSessionChangeWithSeq (mitto-c36) is the seq-aware sibling used to
	// emit the "context_cleared" pill with a seq reserved upstream in PromptWithMeta
	// BEFORE the user-prompt seq, so the pill orders before the user prompt in the
	// persisted transcript.
	pdRecordSessionChangeWithSeq(seq int64, kind, value, previousValue string)

	// Per-conversation prompt-argument cache (mitto-pchx.3): resolver returns the prompt's
	// declared parameter list (with optional Cache config); Get/Set bridge to the in-memory store.
	pdResolvePromptParameters(name string) []config.PromptParameter
	pdCacheGetArg(promptName, paramName string) (string, bool)
	pdCacheSetArg(promptName, paramName, value string, ttl time.Duration)

	// === New in 2.5-d: post-prompt completion helpers ===

	// Token usage bookkeeping
	pdSetLastUsage(usage *acp.Usage)              // lastUsageMu.Lock + lastUsage = usage + Unlock
	pdAccumulateTokenUsage(tokens int)            // processorManager.AccumulateTokenUsage
	pdAccumulateCumulativeUsage(usage *acp.Usage) // adds usage into cumulative in-memory counters
	pdEstimateTokensFromMessage(msg string) int   // processors.EstimateTokens(msg)
	pdReadLastAgentMessage() string               // ReadEvents + GetLastAgentMessage; returns "" on any error

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

	// === New in mitto-2tm: in-place context flush for FreshContext loop runs ===

	// pdContextFlushCommand returns the agent-native context-flush command (e.g. "/clear")
	// configured for this session's ACP server, or "" when the feature is not configured.
	pdContextFlushCommand() string
	// pdFlushContextInPlace sends the flush command synchronously on the existing ACP session
	// with streaming suppressed so the flush turn stays out of the transcript.
	pdFlushContextInPlace(ctx context.Context) error

	// Cold-start diagnostics (mitto-3mv WI-2). Nil-safe — no-op when the
	// session's cold-start trace has not been begun or has been finalized.
	pdColdPhase(name string, kv ...any)
}

// promptDispatcher is a stateless collaborator holding safe synchronous chunks of
// PromptWithMeta that contain no goto labels and no goroutines.
type promptDispatcher struct{}

// SenderID sentinels for non-human dispatch paths: queued messages (which include
// MCP cross-session sends via mitto_conversation_send_prompt) and loop runs.
const (
	senderIDQueue = "queue"
	senderIDLoop  = "loop-runner"
)

// resolveAndSubstitute covers the top of PromptWithMeta:
//  1. Name-resolution: if meta.PromptName != "", resolve via promptResolver
//     (error if no resolver, or if resolution fails). The resolved body replaces
//     any incoming message — the name always wins (mitto-kt6). This closes the
//     class where a queued row or a non-MCP entry point carries both a
//     placeholder message and a PromptName.
//  2. Cache read/merge: for a named prompt, inject cached argument values into
//     meta.Arguments before template render so .Args includes them at render time.
//  3. Go template rendering (mitto-m7sb.5): fast-path guarded; fail-closed for
//     named/automated dispatches, fail-open for direct human input.
//  4. Record argCount = len(meta.Arguments); build argument metadata and annotate meta.Meta.
//
// Returns (resolvedMessage, argCount, updatedMeta, error). On non-nil error the
// caller should return the error immediately (the two early-return paths are preserved).
func (p promptDispatcher) resolveAndSubstitute(d promptDeps, message string, meta PromptMeta) (string, int, PromptMeta, error) {
	if meta.PromptName != "" {
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

	// Per-conversation prompt-argument cache (mitto-pchx.3): for a named prompt,
	// fill cacheable params missing from meta.Arguments from the cache, then write
	// back supplied cacheable values with their TTL (refreshing on re-supply).
	// Runs BEFORE template render so that .Args (built from meta.Arguments in
	// buildProcessorInput) includes cached values at render time.
	if meta.PromptName != "" {
		if params := d.pdResolvePromptParameters(meta.PromptName); len(params) > 0 {
			// Read/merge: inject fresh cached values for cacheable params not already supplied.
			for _, p := range params {
				if p.Cache == nil {
					continue
				}
				if meta.Arguments != nil {
					if _, ok := meta.Arguments[p.Name]; ok {
						continue
					}
				}
				if v, ok := d.pdCacheGetArg(meta.PromptName, p.Name); ok {
					if meta.Arguments == nil {
						meta.Arguments = make(map[string]string)
					}
					meta.Arguments[p.Name] = v
				}
			}
			// Write-back: persist supplied/merged cacheable values with their TTL.
			for _, p := range params {
				if p.Cache == nil {
					continue
				}
				if meta.Arguments == nil {
					continue
				}
				if v, ok := meta.Arguments[p.Name]; ok {
					ttl, _ := p.Cache.ParsedTTL()
					d.pdCacheSetArg(meta.PromptName, p.Name, v, ttl)
				}
			}
		}
	}

	// Template render (mitto-m7sb.5): runs after name-resolution and cache
	// read/merge, so .Args (built from meta.Arguments) includes cached values.
	// Fast-path guard avoids buildProcessorInput for non-template bodies (the
	// common case).
	if config.HasTemplateSyntax(message) {
		// For shared-process sessions, session/new is deferred to the first prompt
		// (see PrewarmACPSession) so conversation creation never blocks on a busy
		// agent process. Without this, the Model(tag) template func would see a
		// nil agentModels on the very first templated prompt — before the async
		// PromptWithMeta goroutine gets a chance to complete the handshake — and
		// silently resolve no tags. completeDeferredHandshake is idempotent and a
		// cheap no-op once the handshake is done, so it's safe to call here on
		// every templated prompt. Best-effort: on failure the render simply
		// degrades to no model tags, matching pdResolveModelTags' documented
		// fail-open behavior (mitto-dl2).
		if d.pdHasSharedProcess() {
			_ = d.pdCompleteDeferredHandshake()
		}
		input := p.buildProcessorInput(d, message, false, meta)
		tctx := processors.BuildCELContext(input)
		// Wire the PromptText resolver (mitto-85y.3): resolves a workspace-prompt
		// NAME to its full body text at render time. Uses the same PromptResolver
		// the dispatcher uses for named-prompt resolution, bound to the current
		// working directory. Nil resolver leaves ctx.PromptTextResolver nil, so
		// PromptText fails-closed with a clear "no resolver available" error.
		if resolver := d.pdPromptResolver(); resolver != nil {
			workingDir := d.pdWorkingDir()
			tctx.PromptTextResolver = func(name string) (string, error) {
				return resolver(name, workingDir)
			}
		}
		funcs := config.BuildTemplateFuncMap(tctx)
		name := meta.PromptName
		if name == "" {
			name = "prompt"
		}
		rendered, rerr := config.RenderPromptTemplate(name, message, tctx, funcs)
		if rerr != nil {
			// Named prompts and loop-runner dispatches always fail-closed. Queue
			// dispatches now distinguish origin: agent-originated (cross-session/MCP
			// sends via mitto_conversation_send_prompt, or MCP-created initial
			// prompts) fail-closed so a broken template body is never silently
			// delivered raw to a child that cannot act on it — that cascaded into a
			// 10m child-wait timeout (mitto-e7u). User-originated queue dispatches
			// (human-typed free text that got queued) fail-open like direct human
			// input, so pasted text containing {{ is delivered literally (mitto-nvb).
			failClosed := meta.PromptName != "" || meta.SenderID == senderIDLoop ||
				(meta.SenderID == senderIDQueue && meta.QueueOrigin == session.QueueOriginAgent)
			if failClosed {
				return "", 0, meta, rerr
			}
			// free-text (direct human input, or user-originated queue dispatch): fail-open — warn and deliver raw message
			if l := d.pdLogger(); l != nil {
				l.Warn("free-text template render failed, delivering raw message",
					"session_id", d.pdSessionID(),
					"error", rerr)
			}
		} else {
			message = rendered
		}
	}

	argCount := len(meta.Arguments)

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
	var hasMessages bool

	if d.pdHasStore() {
		if sessionMeta, err := d.pdGetSessionMetadata(); err == nil {
			sessionName = sessionMeta.Name
			acpServer = sessionMeta.ACPServer
			parentSessionID = sessionMeta.ParentSessionID
			advancedSettings = sessionMeta.AdvancedSettings
			beadsIssue = sessionMeta.BeadsIssue
			hasMessages = !sessionMeta.LastUserMessageAt.IsZero()
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
					BeadsIssue:  child.BeadsIssue,
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
	var userDataMap map[string]string
	if d.pdHasStore() {
		if ud, err := d.pdGetUserData(); err == nil && ud != nil && len(ud.Attributes) > 0 {
			if udBytes, err := json.Marshal(ud.Attributes); err == nil {
				userDataJSON = string(udBytes)
			}
			userDataMap = make(map[string]string, len(ud.Attributes))
			for _, attr := range ud.Attributes {
				userDataMap[attr.Name] = attr.Value
			}
		}
	}

	// Resolve the CURRENT model's capability tags (config models: profiles) for the
	// Model(tag) template func and Session.HasModelTag CEL macro. Degrades to empty
	// (no tags) when agentModels is nil — never errors the render. See mitto-i5sr.
	var modelName string
	var modelTags []string
	if models := d.pdGetAgentModels(); models != nil {
		modelName = ModelDisplayName(models, string(models.CurrentModelId))
		modelTags = d.pdResolveModelTags(modelName)
	}

	// Thread the onTasks trigger delta (if any) through so the {{ .Trigger.OnTasks.* }}
	// template namespace can render — nil for all non-onTasks dispatches (mitto-xkn).
	var triggerOnTasksChanges *config.TasksDelta
	if meta.Trigger != nil && meta.Trigger.OnTasks != nil {
		triggerOnTasksChanges = meta.Trigger.OnTasks.Changes
	}

	return &processors.ProcessorInput{
		Message:                message,
		IsFirstMessage:         isFirst,
		HasMessages:            hasMessages,
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
		IsLoop:                 meta.SenderID == senderIDLoop,
		IsLoopForced:           meta.IsLoopForced,
		IsLoopRunOnStart:       meta.IsLoopRunOnStart,
		IterationNumber:        meta.IterationNumber,
		MaxIterations:          meta.MaxIterations,
		IterationUninterrupted: meta.IterationUninterrupted,
		TriggerOnTasksChanges:  triggerOnTasksChanges,
		Arguments:              meta.Arguments,
		AdvancedSettings:       advancedSettings,
		HasUserDataSchema:      hasUserDataSchema,
		HasMittoRC:             hasMittoRC,
		HasMetadataDescription: hasMetadataDescription,
		UserDataSchemaJSON:     userDataSchemaJSON,
		UserDataJSON:           userDataJSON,
		UserData:               userDataMap,
		ModelTags:              modelTags,
		ModelName:              modelName,
		ProcessorArgOverrides:  d.pdWorkspaceProcessorArgOverrides(),
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

// handshakeWatchdogFallback is the outer wall-clock bound applied to each deferred
// handshake attempt when the shared process reports no recommended timeout
// (mitto-f51). Sized above the normal cold-start budget so a warm-path attempt is
// never truncated, and above the normal per-attempt create budget (~25s) so the
// abort branch only fires on a genuinely wedged session/new. Var (not const) so
// tests can shrink it without waiting the full production duration.
var handshakeWatchdogFallback = 90 * time.Second

// handshakeWatchdogMargin is added on top of RecommendedLoadTimeout so the outer
// wait outlasts the SharedACPProcess's own internal retry loop and never
// prematurely aborts a handshake the process is still legitimately working on.
// Var (not const) so tests can shrink it.
var handshakeWatchdogMargin = 30 * time.Second

// completeHandshakeOrAbort handles the deferred session/new handshake for shared-process
// sessions at the top of the PromptWithMeta goroutine. Returns true to continue, false to
// abort (caller must return from the goroutine). When no shared process is configured it
// is always a no-op that returns true.
//
// Each handshake attempt is bounded by a deadline (mitto-f51) so a hung
// session/new takes the abort branch instead of latching isPrompting silently
// forever. The deadline is derived from the shared process's own recommended
// load timeout (extended for cold MCP handshakes) plus a margin; if the process
// signals no recommendation, handshakeWatchdogFallback is used.
func (p promptDispatcher) completeHandshakeOrAbort(d promptDeps) bool {
	if !d.pdHasSharedProcess() {
		return true
	}

	watchdog := d.pdRecommendedHandshakeDeadline()
	if watchdog > 0 {
		watchdog += handshakeWatchdogMargin
	} else {
		watchdog = handshakeWatchdogFallback
	}

	const maxHandshakeAttempts = 3
	var handshakeErr error
	for attempt := 1; attempt <= maxHandshakeAttempts; attempt++ {
		handshakeErr = runHandshakeWithWatchdog(d, watchdog)
		if handshakeErr == nil {
			break
		}
		// A watchdog trip means the previous attempt's goroutine is still running
		// against the shared process; retrying here would just spawn another that
		// re-blocks the same way. Bail out and let the user re-send (mitto-f51).
		if errors.Is(handshakeErr, errHandshakeWatchdogFired) {
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
	var friendlyMsg string
	if errors.Is(handshakeErr, errHandshakeWatchdogFired) {
		friendlyMsg = "The agent is still starting up — please resend your message."
	} else {
		friendlyMsg = "Could not start the agent session: " + mittoAcp.FormatACPError(handshakeErr) + " Please resend your message."
	}
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

// errHandshakeWatchdogFired signals that runHandshakeWithWatchdog aborted a
// pdCompleteDeferredHandshake attempt because the outer deadline expired
// (mitto-f51). The orphaned goroutine may keep running; the abort branch in
// completeHandshakeOrAbort still clears prompting state so the user is unwedged.
var errHandshakeWatchdogFired = errors.New("deferred session/new timed out (handshake watchdog fired)")

// runHandshakeWithWatchdog invokes pdCompleteDeferredHandshake in a goroutine
// and waits for it up to deadline. On expiry it returns errHandshakeWatchdogFired
// while the goroutine keeps running (its own RPC budget bounds it eventually).
func runHandshakeWithWatchdog(d promptDeps, deadline time.Duration) error {
	if deadline <= 0 {
		return d.pdCompleteDeferredHandshake()
	}
	resultCh := make(chan error, 1)
	go func() { resultCh <- d.pdCompleteDeferredHandshake() }()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case err := <-resultCh:
		return err
	case <-timer.C:
		if l := d.pdLogger(); l != nil {
			l.Warn("Deferred session/new watchdog fired; aborting handshake attempt",
				"session_id", d.pdSessionID(),
				"deadline", deadline)
		}
		return errHandshakeWatchdogFired
	}
}

// createFreshContextSession prepares a fresh context for a FreshContext loop run.
//
// When a contextFlushCommand is configured for the ACP server, it performs an
// in-place flush (sends the command on the existing session with streaming suppressed)
// rather than creating a new ACP session. This works for both direct-conn and
// shared-process sessions. The flush is best-effort: errors are logged as warnings
// but never abort the main loop prompt. Returns "" in this path — the main
// Prompt() continues on the existing session.
//
// When no flush command is configured, falls back to the original NewSession path
// (direct-conn only, gated by pdHasACPConn). Returns the new session ID on success,
// or "" on failure or when FreshContext is not requested.
//
// pillSeq (mitto-c36) is a seq reserved upstream in PromptWithMeta BEFORE the
// user-prompt seq is allocated. When > 0, the "context_cleared" pill is recorded
// with this reserved seq so it orders before the user prompt in the persisted
// transcript. When 0 (never in the production path, only in tests that don't care),
// falls back to the plain pdRecordSessionChange which allocates its own seq.
func (p promptDispatcher) createFreshContextSession(d promptDeps, meta PromptMeta, pillSeq int64) string {
	if !meta.FreshContext {
		return ""
	}

	recordPill := func(value string) {
		if pillSeq > 0 {
			d.pdRecordSessionChangeWithSeq(pillSeq, "context_cleared", value, "")
			return
		}
		d.pdRecordSessionChange("context_cleared", value, "")
	}

	// Prefer in-place flush when the ACP server has a flush command configured.
	if cmd := d.pdContextFlushCommand(); cmd != "" {
		flushCtx, flushCancel := context.WithTimeout(d.pdSessionCtx(), 30*time.Second)
		err := d.pdFlushContextInPlace(flushCtx)
		flushCancel()
		if err == nil {
			if l := d.pdLogger(); l != nil {
				l.Info("In-place context flush succeeded for loop FreshContext run",
					"session_id", d.pdSessionID())
			}
			// Surface the context clear in the conversation timeline (mitto-so19).
			recordPill("flush")
		} else {
			if l := d.pdLogger(); l != nil {
				l.Warn("In-place context flush failed, continuing with main prompt",
					"error", err,
					"session_id", d.pdSessionID())
			}
		}
		// Always return "" — main prompt continues on the existing session.
		return ""
	}

	// Fallback: create a new ACP session (direct-conn only).
	if !d.pdHasACPConn() {
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
			l.Info("Created fresh ACP session for loop run",
				"fresh_session_id", sessID,
				"session_id", d.pdSessionID())
		}
		// Surface the context clear in the conversation timeline (mitto-so19).
		recordPill("new_session")
		return sessID
	}
	if l := d.pdLogger(); l != nil {
		l.Warn("Failed to create fresh ACP session, using existing",
			"error", err,
			"session_id", d.pdSessionID())
	}
	return ""
}

// modelSwitchSyncGrace bounds how long applyModelPreference blocks the interactive
// prompt waiting for a set_model RPC to land. A warm switch completes well within
// this window so the preferred model applies to THIS turn; a cold/slow switch
// exceeds it, so the prompt is dispatched on the current model immediately and the
// switch completes in the background (applying to the NEXT turn). Prevents the
// cold-start wedge where a synchronous ~41s set_model blocked the first prompt
// (mitto-54k.5). A var so tests can shrink it.
var modelSwitchSyncGrace = 3 * time.Second

// modelSwitchAsyncBudget is the total wall-clock budget for the background set_model
// RPC, bounded by the session context. Mirrors the aux-session async budget so a
// cold agent gets its full retry schedule off the critical path (mitto-54k.5).
// A var so tests can shrink it.
var modelSwitchAsyncBudget = 90 * time.Second

// applyModelPreference ensures the correct model is active before sending the prompt.
// Implements set-if-different (lazy): only issues a SetSessionModel RPC when the
// desired model differs from the current active model. No-op when agentModels is nil.
func (p promptDispatcher) applyModelPreference(d promptDeps, meta PromptMeta) {
	models := d.pdGetAgentModels()

	preferredModels := meta.PreferredModels
	if len(preferredModels) == 0 && meta.PromptName != "" {
		preferredModels = d.pdResolvePreferredModels(meta.PromptName)
	}

	if models == nil {
		// mitto-ishl: when the agent never advertises a model catalog (e.g.
		// every Auggie session today) a declared preferredModels used to be
		// silently dropped, taking the ⚡ model_override pill with it. Try to
		// resolve the preference against a synthetic state built from the
		// caller's global model profiles so the pill still fires — we cannot
		// issue set_model (no ACP model to switch to), but the pill is a
		// display-only signal of which tier the prompt intended.
		if len(preferredModels) > 0 {
			synth := SynthesizeModelStateFromProfiles(d.pdModelProfiles())
			if synth != nil {
				if resolved := SelectPreferredModel(preferredModels, d.pdModelProfiles(), synth); resolved != "" {
					baseline := d.pdReadBaselineModel()
					d.pdRecordSessionChange(
						ConfigOptionCategoryModelOverride,
						ModelDisplayName(synth, resolved),
						baseline,
					)
					d.pdWriteOverrideActive(true)
					if l := d.pdLogger(); l != nil {
						l.Info("apply_model_preference",
							"session_id", d.pdSessionID(),
							"prompt_name", meta.PromptName,
							"preferred_models", preferredModels,
							"resolved", resolved,
							"decision", "synth_profile_pill")
					}
					return
				}
			}
		}
		if l := d.pdLogger(); l != nil {
			// Preference declared but no profile resolves (or none configured):
			// keep the WARN escalation so tier-switching failures are loud.
			// A genuine no-preference no-op stays at DEBUG.
			if len(preferredModels) > 0 {
				l.Warn("apply_model_preference",
					"session_id", d.pdSessionID(),
					"prompt_name", meta.PromptName,
					"preferred_models", preferredModels,
					"decision", "skip_agent_advertises_no_models")
			} else {
				l.Debug("apply_model_preference",
					"session_id", d.pdSessionID(),
					"prompt_name", meta.PromptName,
					"decision", "skip_no_agent_models")
			}
		}
		return
	}

	baseline := d.pdReadBaselineModel()
	currentModel := string(models.CurrentModelId)
	desired := baseline
	matched := false
	if len(preferredModels) > 0 {
		if resolved := SelectPreferredModel(preferredModels, d.pdModelProfiles(), models); resolved != "" {
			desired = resolved
			matched = true
		}
		// no match → desired stays as baseline (prevents override leakage)
	}

	isOverride := desired != "" && desired != baseline
	switching := desired != "" && desired != currentModel

	finalizeOverride := func(switchFailed bool) {
		if isOverride && !switchFailed {
			d.pdRecordSessionChange(
				ConfigOptionCategoryModelOverride,
				ModelDisplayName(models, desired),
				ModelDisplayName(models, baseline),
			)
		}
		d.pdWriteOverrideActive(isOverride)
	}

	if l := d.pdLogger(); l != nil {
		decision := "switching"
		if !switching {
			switch {
			case len(preferredModels) == 0:
				decision = "skip_no_preference"
			case !matched:
				decision = "skip_no_match"
			default:
				decision = "skip_already_satisfied"
			}
		}
		l.Debug("apply_model_preference",
			"session_id", d.pdSessionID(),
			"prompt_name", meta.PromptName,
			"preferred_models", preferredModels,
			"baseline", baseline,
			"current_model", currentModel,
			"desired", desired,
			"decision", decision)
	}

	if !switching {
		finalizeOverride(false)
		return
	}

	// A model switch is required. Do NOT block the interactive prompt on a slow
	// set_model (mitto-54k.5): run the switch in the background bounded by the
	// session context, but wait up to modelSwitchSyncGrace for it to land so a warm
	// switch still applies to THIS turn. On a cold/slow agent the grace elapses, the
	// prompt is dispatched on the current model, and the switch completes in the
	// background (applying to the NEXT turn). setModelSem serialisation and the
	// mitto-29q re-arm are preserved because the switch still goes through
	// pdSetActiveModelOnly -> SetSessionModel.
	switchStart := time.Now()
	done := make(chan struct{})
	go func() {
		setCtx, setCancel := context.WithTimeout(d.pdSessionCtx(), modelSwitchAsyncBudget)
		defer setCancel()
		setErr := d.pdSetActiveModelOnly(setCtx, desired)
		if setErr != nil {
			if l := d.pdLogger(); l != nil {
				l.Warn("Failed to apply model preference", "model", desired, "error", setErr)
			}
		}
		// finalize BEFORE signalling done so the warm path observes the pill and
		// override flag as soon as the select returns (happens-before via close(done)).
		finalizeOverride(setErr != nil)
		close(done)
	}()

	select {
	case <-done:
		// Switch landed within the grace window (warm/fast): applies to this turn.
		d.pdColdPhase("model_switch",
			"desired", desired,
			"from", currentModel,
			"landed", "warm",
			"switch_ms", time.Since(switchStart).Milliseconds())
	case <-time.After(modelSwitchSyncGrace):
		// Cold/slow: dispatch the prompt now; the switch completes in the background.
		if l := d.pdLogger(); l != nil {
			l.Info("Deferring model switch to background; dispatching prompt on current model",
				"session_id", d.pdSessionID(),
				"desired_model", desired,
				"current_model", currentModel)
		}
		d.pdColdPhase("model_switch",
			"desired", desired,
			"from", currentModel,
			"landed", "deferred",
			"grace_ms", modelSwitchSyncGrace.Milliseconds())
	}
}

// accumulateTokenUsage stores and accumulates token usage from a prompt response.
// When the response includes usage, it stores it and accumulates the total tokens.
// When usage is absent, it falls back to text-based estimation from the message
// and the last agent response.
func (p promptDispatcher) accumulateTokenUsage(d promptDeps, promptResp acp.PromptResponse, message string) {
	if promptResp.Usage != nil {
		d.pdSetLastUsage(promptResp.Usage)
		d.pdAccumulateCumulativeUsage(promptResp.Usage)
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

	// Retry title generation if session still has no title.
	d.pdRetryTitleGenerationIfNeeded(message)

	// Read the last agent message once and reuse for both follow-up analysis
	// and the sessionIdle gate below (mitto-vn3). Cheap when store is nil.
	agentMessage := d.pdReadLastAgentMessageFromStore()

	// Async follow-up analysis (non-blocking).
	isEndTurn := promptResp.StopReason == acp.StopReasonEndTurn
	if d.pdActionButtonsEnabled() && isEndTurn {
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
	// The agentIdle flag here is queue-drain only; it is intentionally NOT
	// gated on turn semantics so the after-processors pipeline keeps firing
	// for every terminal turn (including cancels / max_turn_requests).
	d.pdApplyAfterProcessors(d.pdSessionCtx(), message, meta.SenderID,
		string(promptResp.StopReason), promptStartedAt, promptEndedAt, promptResp, !dispatched)

	// sessionIdle gates the on-completion loop hook (pdOnTurnIdle → LoopRunner
	// armCompletionTimer). It must be true only when the turn actually reached
	// a semantic end (endTurn) AND the agent produced some assistant text —
	// otherwise a tool-only or degenerate endTurn-with-no-text turn silently
	// re-arms the loop and drives a runaway (mitto-vn3).
	sessionIdle = !dispatched && isEndTurn && agentMessage != ""
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

	// Notify the on-completion loop hook once the agent has stopped and the
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
			if classified, ok := restartErr.(*mittoAcp.ACPClassifiedError); ok && !classified.IsRetryable() {
				errMsg = mittoAcp.FormatClassifiedError(classified)
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
	userFriendlyErr := mittoAcp.FormatACPError(err)
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
	if !mittoAcp.IsContextTooLargeError(err) && !mittoAcp.IsRateLimitError(err) {
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
