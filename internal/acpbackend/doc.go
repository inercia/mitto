// Package acpbackend adapts the existing ACP-over-subprocess runtime
// (internal/acp, internal/acpproc, internal/conversation.SharedProcess) onto
// the protocol-neutral contracts defined in internal/agentbackend
// (mitto-lrt.4), without changing any observable behavior of the existing
// ACP path (mitto-lrt.6).
//
// Dependency direction: acpbackend imports agentbackend (the neutral
// contracts), github.com/coder/acp-go-sdk (the protocol SDK), and
// internal/conversation (for the SharedProcess/SessionHandle/
// SessionCallbacks types the existing ACP runtime already exposes). It is
// the protocol-specific home for the ACP SDK that agentbackend's own import
// guard (imports_test.go) forbids agentbackend itself from depending on.
//
// internal/conversation does NOT import this package: conversation already
// sits above internal/acpproc (acpproc imports conversation for
// SessionHandle/SessionCallbacks), so wiring this adapter into
// BackgroundSession so real Mitto conversations flow through it is deferred
// to mitto-lrt.7 — here the adapter is proven standalone against the
// SharedProcess interface via NewConnection.
//
// See docs/devel/agent-backend-architecture.md for the ADR this adapter
// implements against.
package acpbackend
