package acpbackend

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// Connection adapts an existing conversation.SharedProcess (the ACP-over-
// subprocess runtime managed by internal/acpproc) onto the protocol-neutral
// contracts in internal/agentbackend, without changing any observable
// behavior of the existing ACP path (mitto-lrt.6). It deliberately mirrors
// agentbackend.Connection's own doc comment: subprocess lifecycle concerns
// (starting/restarting the OS process, PID tracking, MCP warmup, GC) stay
// entirely inside internal/acpproc — Connect/Close/State here are lightweight
// bookkeeping over an already-running process, not process management.
//
// Bridging this adapter into internal/conversation.BackgroundSession itself
// (so real Mitto conversations actually flow through it) is deferred to
// mitto-lrt.7; this type is proven standalone against the SharedProcess
// interface for now.
type Connection struct {
	mu    sync.RWMutex
	state agentbackend.LifecycleState

	process    conversation.SharedProcess
	provider   agentbackend.ProviderID
	cwd        string
	mcpServers []acp.McpServer

	// hooks, when non-nil, lets the caller supply real file/permission
	// handling for ClientServices; nil means those requests are rejected
	// with *agentbackend.UnsupportedError rather than silently
	// auto-approved or served (see client_services.go).
	hooks *ClientHooks

	// terminalHooks, when non-nil, lets the caller supply real terminal
	// execution for TerminalServices; nil means those requests are rejected
	// with *agentbackend.UnsupportedError (see terminal_services.go).
	terminalHooks *TerminalHooks

	convSeq int64 // atomic: source for synthesized ConversationIDs (see NewSession)

	subMu sync.Mutex
	subs  map[string][]*subscription // key: ConversationID, "" = host-wide

	sessMu   sync.Mutex
	sessions map[agentbackend.SessionRef]*acpSession
}

// NewConnection wraps an already-running conversation.SharedProcess (obtained
// from the existing acpproc.ACPProcessManager) as a protocol-neutral
// agentbackend Connection for the given provider. cwd and mcpServers are the
// same NewSession/LoadSession parameters the existing ACP path already
// threads through per-session; SessionOps.NewSession's neutral signature
// carries no per-call cwd, so the adapter is configured with one cwd per
// Connection (matching one BackgroundSession owning one Connection, the
// eventual wiring shape from mitto-lrt.7).
func NewConnection(process conversation.SharedProcess, provider agentbackend.ProviderID, cwd string, mcpServers []acp.McpServer) *Connection {
	return &Connection{
		state:      agentbackend.LifecycleDisconnected,
		process:    process,
		provider:   provider,
		cwd:        cwd,
		mcpServers: mcpServers,
		subs:       make(map[string][]*subscription),
		sessions:   make(map[agentbackend.SessionRef]*acpSession),
	}
}

// SetClientHooks installs the optional file/permission delegate used to
// answer ACP fs/permission requests via the neutral ClientServices contract.
// Must be called before NewSession/LoadSession/ResumeSession register a
// session's callbacks for it to take effect for that session.
func (c *Connection) SetClientHooks(hooks *ClientHooks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = hooks
}

// Connect implements agentbackend.Connection.
func (c *Connection) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = agentbackend.LifecycleConnected
	return nil
}

// Close implements agentbackend.Connection. Safe to call multiple times.
func (c *Connection) Close() error {
	c.mu.Lock()
	c.state = agentbackend.LifecycleStopped
	c.mu.Unlock()

	c.subMu.Lock()
	c.subs = make(map[string][]*subscription)
	c.subMu.Unlock()
	return nil
}

// State implements agentbackend.Connection.
func (c *Connection) State() agentbackend.LifecycleState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Providers implements agentbackend.ProviderDiscovery. The ACP protocol runs
// exactly one agent per process, so this always reports the single
// configured provider.
func (c *Connection) Providers(ctx context.Context) ([]agentbackend.ProviderID, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return []agentbackend.ProviderID{c.provider}, nil
}

// Ownership implements agentbackend.ResourceOwner. The ACP protocol always
// spawns a local subprocess for the agent (internal/acpproc), so every ACP
// session's file/terminal requests describe resources on the LOCAL Mitto
// machine — this is a constant fact of the ACP transport, not something to
// infer per-session, and preserves the existing byte-identical local
// execution behavior of the pre-mitto-lrt.11 ACP path.
func (c *Connection) Ownership(ref agentbackend.SessionRef) agentbackend.ResourceOwnership {
	return agentbackend.OwnershipLocal
}

// nextConversationID synthesizes a Mitto-owned conversation id for a
// newly-created session. SessionOps.NewSession's neutral signature (ctx,
// provider) carries no caller-supplied conversation id, so the adapter must
// generate one itself until real Mitto conversation ids are threaded through
// by the wiring work in mitto-lrt.7 (deliberately distinct from the upstream
// ProviderSession id — see agentbackend's SessionRef ADR §4 — rather than
// reusing it for both fields).
func (c *Connection) nextConversationID() string {
	n := atomic.AddInt64(&c.convSeq, 1)
	return "acpbackend-conv-" + strconv.FormatInt(n, 10)
}

var (
	_ agentbackend.Connection        = (*Connection)(nil)
	_ agentbackend.ProviderDiscovery = (*Connection)(nil)
	_ agentbackend.ResourceOwner     = (*Connection)(nil)
)
