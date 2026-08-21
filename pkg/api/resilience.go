package api

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Resilience configuration for Session. All of it is opt-in and off by
// default (zero value = disabled), so Connect's existing behavior — one
// dial, one read loop, no extra outgoing frames — is unchanged for every
// caller that does not pass these options. See docs/devel/go-client-library.md
// §6 and docs/devel/websockets/{sequence-numbers,synchronization}.md for the
// contract this mirrors from the browser client.

// ReconnectConfig enables automatic reconnection with exponential backoff.
// A zero-value ReconnectConfig (Enabled: false) disables reconnection
// entirely, matching today's single-shot readLoop behavior.
type ReconnectConfig struct {
	// Enabled turns on the reconnect supervisor. Default: false.
	Enabled bool

	// BaseDelay is the initial backoff delay. Default 1s if zero.
	BaseDelay time.Duration

	// MaxDelay caps the backoff delay. Default 30s if zero.
	MaxDelay time.Duration

	// JitterFactor adds up to this fraction of random jitter on top of the
	// exponential delay (0.3 == up to 30% extra). Default 0.3 if zero.
	JitterFactor float64

	// jitter is an injectable jitter source for deterministic tests.
	// Defaults to rand.Float64 (returns a value in [0,1)).
	jitter func() float64
}

// withDefaults returns a copy of cfg with zero fields filled with the same
// constants used by the browser client (docs/devel/websockets/synchronization.md
// "Exponential Backoff (M2 fix)").
func (cfg ReconnectConfig) withDefaults() ReconnectConfig {
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 1 * time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.JitterFactor <= 0 {
		cfg.JitterFactor = 0.3
	}
	if cfg.jitter == nil {
		cfg.jitter = rand.Float64
	}
	return cfg
}

// reconnectDelay computes the backoff delay for the given attempt number
// (0-based: the first retry is attempt 0). Pure function, no sleeping, so it
// is directly unit-testable. Mirrors calculateReconnectDelay in
// web/static/hooks/useWebSocket.js.
func reconnectDelay(attempt int, cfg ReconnectConfig) time.Duration {
	cfg = cfg.withDefaults()
	exp := float64(cfg.BaseDelay) * math.Pow(2, float64(attempt))
	if exp > float64(cfg.MaxDelay) {
		exp = float64(cfg.MaxDelay)
	}
	jitter := exp * cfg.JitterFactor * cfg.jitter()
	return time.Duration(exp + jitter)
}

// KeepaliveConfig enables application-level keepalive heartbeats and
// zombie-connection detection. A zero-value KeepaliveConfig (Enabled: false)
// disables keepalives entirely.
type KeepaliveConfig struct {
	// Enabled turns on the keepalive ticker. Default: false.
	Enabled bool

	// Interval between keepalive sends. Default 10s if zero.
	Interval time.Duration

	// MaxMissed is the number of consecutive un-acked keepalives tolerated
	// before the connection is declared a zombie and force-closed. Default
	// 2 if zero, matching KEEPALIVE_MAX_MISSED in useWebSocket.js.
	MaxMissed int
}

func (cfg KeepaliveConfig) withDefaults() KeepaliveConfig {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.MaxMissed <= 0 {
		cfg.MaxMissed = 2
	}
	return cfg
}

// SeqStore persists the reconnection sequence-number watermark for a
// session, mirroring the browser's localStorage tier
// (docs/devel/websockets/sequence-numbers.md "three-tier watermark"). The
// default in-memory implementation (see memorySeqStore) is used when no
// store is supplied via WithSeqStore, so persistence across process
// restarts is opt-in for callers that need it.
//
// Store sets the watermark exactly to seq (it does not clamp to a running
// maximum): Session only ever calls Store with an already-monotonic value
// on the happy path (observeSeq), and calls it with 0 to deliberately reset
// a stale client's watermark (docs/devel/websockets/sequence-numbers.md
// "Server is Always Right" / "Stale Client Reset"). A SeqStore
// implementation must not silently ignore a lower value.
type SeqStore interface {
	// Load returns the last known watermark for sessionID, or 0 if none is
	// stored yet.
	Load(sessionID string) (int64, error)

	// Store records seq as the watermark for sessionID, replacing any
	// previous value.
	Store(sessionID string, seq int64) error
}

// memorySeqStore is the default in-memory SeqStore, safe for concurrent use.
type memorySeqStore struct {
	mu   sync.Mutex
	seqs map[string]int64
}

// NewMemorySeqStore returns a process-local, in-memory SeqStore. This is the
// default used by Session when WithSeqStore is not passed to Connect.
func NewMemorySeqStore() SeqStore {
	return &memorySeqStore{seqs: make(map[string]int64)}
}

func (m *memorySeqStore) Load(sessionID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seqs[sessionID], nil
}

func (m *memorySeqStore) Store(sessionID string, seq int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seqs[sessionID] = seq
	return nil
}

// defaultStreamBuffer is the default bounded capacity of the internal
// channel feeding Session.Events/EventsChan (see stream.go). Overridable
// via WithStreamBuffer.
const defaultStreamBuffer = 256

// resilienceConfig aggregates all SessionOption settings. The zero value
// disables every resilience feature, preserving today's Connect behavior.
type resilienceConfig struct {
	reconnect    ReconnectConfig
	keepalive    KeepaliveConfig
	seqStore     SeqStore
	dedup        bool
	streamBuffer int
}

// SessionOption configures optional resilience behavior on Connect. Options
// are opt-in: a Session created with no options behaves exactly as before
// (single dial, single read loop, no keepalive, no dedup, no seq tracking).
type SessionOption func(*resilienceConfig)

// WithReconnect enables automatic reconnection with exponential backoff on
// unexpected disconnects. See ReconnectConfig for tunables.
func WithReconnect(cfg ReconnectConfig) SessionOption {
	cfg.Enabled = true
	return func(rc *resilienceConfig) { rc.reconnect = cfg }
}

// WithKeepalive enables periodic application-level keepalive heartbeats and
// zombie-connection detection. See KeepaliveConfig for tunables.
func WithKeepalive(cfg KeepaliveConfig) SessionOption {
	cfg.Enabled = true
	return func(rc *resilienceConfig) { rc.keepalive = cfg }
}

// WithSeqStore supplies a pluggable store for the sequence-number watermark
// used to resync after a reconnect (load_events{after_seq}). If not
// supplied, an in-memory store scoped to the Session is used.
func WithSeqStore(store SeqStore) SessionOption {
	return func(rc *resilienceConfig) { rc.seqStore = store }
}

// WithSeqDedup enables client-side deduplication of inbound events by
// sequence number, allowing same-seq events through for streaming
// coalescing (docs/devel/websockets/sequence-numbers.md). Typically paired
// with WithReconnect so re-delivered events after a resync are dropped.
func WithSeqDedup(enabled bool) SessionOption {
	return func(rc *resilienceConfig) { rc.dedup = enabled }
}

// WithStreamBuffer overrides the bounded capacity of the internal channel
// feeding Session.Events/EventsChan (default 256, see defaultStreamBuffer).
// A non-positive size is ignored (the default is kept). Sizing this too
// small increases the chance of ErrSlowConsumer for bursty producers; it
// does not affect SessionCallbacks delivery, which is unbuffered/blocking
// as before.
func WithStreamBuffer(size int) SessionOption {
	return func(rc *resilienceConfig) {
		if size > 0 {
			rc.streamBuffer = size
		}
	}
}
