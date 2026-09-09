// Package eventprojection implements neutral event projection and durable
// upstream replay reconciliation on top of internal/agentbackend's neutral
// Event contract (see docs/devel/agent-backend-architecture.md, mitto-lrt.8).
//
// A Projector consumes agentbackend.Event values for a single upstream
// source, coalesces partial agent-message/agent-thought chunks to a logical
// boundary, allocates Mitto-owned sequence numbers at commit time (via
// SeqAllocator), and reconciles reconnect/replay gaps against a durable
// Checkpoint (persisted through the CheckpointStore seam). A separate
// PromptCorrelation tracks optimistic local prompt IDs against upstream
// echoes so a confirmed echo of local work is not re-projected as a new
// externally-originated action, while genuinely external updates are
// projected exactly once with SuppressLocalAutomation set so downstream
// processors/loops do not double-fire on them.
//
// This package is purely additive and protocol-neutral: it imports
// internal/agentbackend only and must never import a protocol-specific SDK
// (e.g. github.com/coder/acp-go-sdk), internal/acp, internal/acpproc,
// internal/web, internal/conversation, internal/session, or os/exec — see
// imports_test.go for the enforcing guard. It is proven only against the
// in-memory fakes in this package and agentbackend.FakeHost; a durable,
// session-sidecar-backed CheckpointStore implementation lives in the
// sibling package internal/eventprojection/eventprojectionsession (kept
// outside this leaf so the import guard never has to allow
// internal/session). Live wiring into BackgroundSession/SessionManager is
// deferred to later work (mitto-lrt.12+); this package is not imported by
// internal/conversation in this increment.
//
// Replay/dedup guarantee, precisely stated: reconciling a Checkpoint gives
// AT-MOST-ONCE PROJECTION of a given upstream identity into a Sink — it is
// NOT a guarantee of exactly-once SIDE-EFFECT EXECUTION by whatever consumes
// the Sink's output. Callers that need exactly-once side effects must add
// their own idempotency on top (e.g. keyed by ProjectedEvent.UpstreamIdentity
// or ProjectedEvent.Seq).
package eventprojection
