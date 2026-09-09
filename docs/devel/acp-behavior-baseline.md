# ACP Behavior Regression Baseline (mitto-lrt.2)

This document is the executable regression baseline recorded **before** any
runtime or wire-shape changes to the ACP boundary (mitto-lrt epic). It exists
so a later backend-boundary extraction can be verified behavior-preserving:
every row below either cites an existing passing test or a newly-added one:
"pin it, then refactor under it."

Survey-first, add-only: the coverage survey found substantial existing
coverage already in place, so this baseline adds only the genuinely-missing
boundary cases identified below, without touching any existing assertion.

## Provenance

- **Source revision:** `b3f2091f631cd6b3a728abf99045a194d4b62462` (2026-09-07 17:45:27 +0200).
- **Toolchain:** go1.27.1 (go.mod directive: `go 1.25.8`).
- **Agents exercised:** Auggie 0.35.0 (commit 9a7f3836); Claude Code 2.1.263.
- **Harness:** `tests/mocks/acp-server` (synthetic, scripted responses) +
  `tests/fixtures/responses/*.json` scenarios — never a live agent or account.

## Methodology

```bash
make build-mock-acp                                       # required before every run below
go test -tags integration ./tests/integration/inprocess/... # full baseline
go test -tags integration -race ./tests/integration/inprocess/... -run 'Lifecycle|Sequence|Streaming'
```

Startup/response-latency and resource-use comparisons: the `cold_start_summary`
log line emitted by every session start (see `internal/web` cold-start
tracing) already records `total_ms` and per-phase `phase_ms`/`rpc_ms` for
`session/new`, `session/resume`, and `session/load`. A material regression is
any sustained increase in `cold_start_summary.total_ms` (or a per-phase
`rpc_ms`) for the mock-ACP harness across a before/after comparison at the
same git revision of the harness — no separate benchmark harness is
introduced here; the existing structured logs are the reproducible baseline.

Any pre-existing failure in the full suite (unrelated to this baseline) must
be recorded in [Pre-existing failures](#pre-existing-failures) below, never
silently used to excuse a *new* regression.

## Coverage matrix

Each row: preserved ACP behavior → the test(s) that pin it. "NEW" marks tests
added by this increment; all others already existed at the baseline revision.

| Behavior | Test(s) |
| --- | --- |
| session/new happy path, deferred to first prompt | `lazy_session_test.go::TestLazyACPSessionCreation` |
| session/load fallback chain (resume→load→new) | `tests/mocks/acp-server/RESUME_CAPABILITY.md` (documented chain); `session_resume_test.go`, `resume_constraint_test.go` |
| Resume restores manual model selection across archive→unarchive | `resume_constraint_test.go::TestResumeModelConstraint_PreservesManualSelection` |
| Resume ordering vs. queued prompts | `resume_constraint_test.go::TestResumeModelConstraint_LandsBeforeQueuedPrompt` |
| Archive → unarchive → **re-archive** (2nd cycle) stays functional | **NEW** `lifecycle_baseline_test.go::TestLifecycleBaseline_ArchiveUnarchiveRearchive` |
| Startup recovery replays persisted history after a full server restart | **NEW** `lifecycle_baseline_test.go::TestLifecycleBaseline_StartupRecoveryReplaysEvents` |
| Session deletion signal + WS close code | `sequence_contract_test.go::TestWebSocketCloseCode_SessionDeleted` |
| Shared-process generation-fenced restart after a single crash | `restart_test.go::TestACPRestart_SingleCrash` |
| Saturation / admission control under concurrent cold starts | `coldstart_contention_test.go`, `thundering_herd_test.go` |
| Sequence monotonicity / persistence / reconnect sync | `sequence_contract_test.go::TestSequenceNumberMonotonicity`, `TestSequenceNumberPersistence`, `TestSequenceNumberSyncAfterReconnect` |
| Multiple clients see identical seqs | `sequence_contract_test.go::TestMultipleClientsReceiveSameSeqs` |
| `connected` always precedes `events_loaded` (incl. reconnect) | `sequence_contract_test.go::TestProtocolOrdering_ConnectedBeforeEventsLoaded[_MultipleReconnects]` |
| Keepalive wire ack carries `max_seq` | `sequence_contract_test.go::TestKeepalive_AckReceivedForKeepaliveMessage` |
| `after_seq == current max_seq` boundary returns zero events | **NEW** `streaming_seq_baseline_test.go::TestStreamingBaseline_AfterSeqEqualsMaxSeqReturnsNoEvents` |
| Cancel() called mid-stream ends the turn without waiting on the agent | **NEW** `streaming_seq_baseline_test.go::TestStreamingBaseline_CancelDuringActiveStreaming` |
| Cancel() during preparation (pre-RPC) never persists | `websocket_prompt_preparation_test.go` |
| Prompt ACK / queue transitions | `queue_test.go`, `websocket_test.go` |
| Loop conversations fire on their configured trigger (onCompletion / onTasks / onChild / runOnStart / multi-trigger) and stale-loop handling | `loop_oncompletion_e2e_test.go::TestLoopOnCompletionE2E`, `loop_ontasks_e2e_test.go::TestLoopOnTasksE2E`, `loop_onchild_e2e_test.go::TestLoopOnChildE2E`, `loop_runonstart_e2e_test.go`, `loop_multitrigger_runonstart_e2e_test.go`, `stale_loop_test.go` |
| Slow client / backpressure | `slow_client_test.go`, `websocket_message_size_test.go` |
| Images in prompts | `image_prompt_test.go` |
| Tool call `completed` status, interleaved with messages/thoughts | `tests/fixtures/responses/tool-calls-interleaved.json` |
| Tool call `failed` status relayed live AND persisted verbatim | **NEW** `acp_semantics_baseline_test.go::TestSemanticsBaseline_ToolCallFailedStatusPreserved` + `tests/fixtures/responses/tool-call-failed.json` |
| Permission requests: auto-approve modes, user selection, error/no-observer cancellation; terminal permission flow | `internal/acp/permission_test.go`, `internal/acp/terminal_test.go`, `internal/acp/acp_callback_sink_test.go` (`TestCallbackSink_Permission_*`) |
| Model changes (session_change events) | `model_lifecycle_test.go::TestConversationModelLifecycle` |
| Transient per-prompt model override apply + `restoreBaselineIfOverride` restore (no stray `session_change`) | `internal/conversation/prompt_dispatcher_test.go` (`TestPromptDispatcher_ApplyModelPreference_*`, unit-level with fakes); `internal/conversation/background_session_test.go`, `config_manager_test.go` |
| Legacy `set_model` fallback for pre-0.13-schema agents | `set_model_legacy_fallback_test.go` |
| Concurrent `set_model` bursts | `concurrent_model_set_test.go` |
| Deferred config application ordering vs. queued prompts | `deferred_config_test.go` |
| Legacy config load/save round-trips (settings load, defaults, config↔settings round-trip incl. prompts/session) | `internal/config/settings_test.go` (`TestLoadSettings_*`, `TestConfigToSettings_RoundTrip*`) |
| Authenticated per-conversation MCP injection / init timeout | `mcp_init_timeout_test.go` |
| MCP transport-binding dissociation on conversation close (single owner, shared-lease multi-owner retention/retirement, in-flight POST draining, idempotent cleanup, new-owner-cancels-retirement, idle-while-owned) | `internal/mcpserver/mcp_session_reaper_test.go` (`TestReapOwnerlessMCPSessionWithOpenStream`, `TestReapSharedMCPSessionAfterFinalOwner`, `TestReapOwnerlessMCPSessionWaitsForInflightPOST`, `TestPOSTCancelsPendingOwnerRetirement`, `TestNewOwnerCancelsPendingOwnerRetirement`, `TestOwnerLifecycleCleanupIsIdempotent`, `TestOwnedMCPSessionIsNotIdleReapedPastTimeout`) |
| Auxiliary (hidden) session work: title generation, prompt improvement, follow-up analysis, MCP watchers, processor completion | `internal/auxiliary/workspace_manager_test.go` (`TestWorkspaceAuxiliaryManager_*`), `internal/auxiliary/mcp_watchers_test.go`, `internal/auxiliary/processor_completion_test.go` |
| SDK (`pkg/api`) contract | `sdk_contract_test.go` |
| WS reconnect dedup / pruning | `prune_reconnect_test.go` |

## Pre-existing failures

None observed in `tests/integration/inprocess/...` at the baseline revision
under the methodology above. If a pre-existing failure is found by a later
phase, it must be added here (with the failing test name and a link to its
tracking bead) rather than used to excuse a new regression.

## Non-goals of this baseline

Per the mitto-lrt.2 acceptance criteria, this document and its tests are a
**behavior-preserving characterization**, not a functional change: no new
user workflow, backend/protocol picker, or production AHP adapter ships as
part of this baseline. Fixtures contain synthetic data only.
