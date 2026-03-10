# Backend Architecture — BackgroundSession & Observer Pattern

## BackgroundSession (`internal/web/background_session.go`)

This is the most complex file in the codebase (~3500 lines). It manages the lifecycle of an ACP agent session.

### Key Responsibilities
- Starts and manages the ACP subprocess (Claude Code CLI)
- Bridges WebSocket clients to the ACP agent via the observer pattern
- Handles image uploads, base64 encoding, and content block assembly
- Manages prompt queuing, history injection, and processor execution
- Stores agent capabilities from ACP initialization

### Observer Pattern
- `SessionObserver` interface defined in `observer.go`
- Observers are registered via `AddObserver()` — must call `LoadEvents` from WebSocket first
- Notify all observers: `bs.notifyObservers(func(o SessionObserver) { o.OnXxx(...) })`
- Key callbacks: `OnAgentMessage(seq, html)`, `OnError(message)`, `OnToolCall(seq, id, title, status)`
- `OnError` is for user-facing warnings/errors (no seq number needed)
- `OnAgentMessage` requires a sequence number and persists to `events.jsonl`

### PromptWithMeta Flow
1. Validate capabilities (e.g., image support)
2. Load images from disk, base64 encode → `acp.ImageBlock()`
3. Run command processors (pre-processing)
4. Build `finalBlocks` array: image blocks + processor blocks + text block
5. Log content block summary
6. Launch goroutine: create prompt context → send to ACP → stream response

### Adding New Features to BackgroundSession
- Add private field + public getter
- Set field during ACP initialization (in `doStartACPProcess`)
- Use field in `PromptWithMeta` with appropriate checks
- Expose via `sendSessionConnected()` in `session_ws.go`

## SessionManager (`internal/web/session_manager.go`)

- Registry of active sessions: `activeSessions map[string]*managedSession`
- Protected by `mu sync.RWMutex`
- Access via methods: `GetSession()`, `GetOrCreateSession()`, `RemoveSession()`
- Do NOT access `activeSessions` directly from other files — use the accessor methods

## Server (`internal/web/server.go`)

- HTTP server with embedded static assets from `web/static/`
- `Handler()` method exposes the router for `httptest.Server` in tests
- Dual listener: localhost (no auth) + optional external (with auth)
