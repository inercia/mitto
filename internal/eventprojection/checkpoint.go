package eventprojection

import "errors"

// ErrCheckpointNotFound is returned by CheckpointStore.Load when no
// checkpoint has been persisted yet for a SourceID. This is a normal,
// expected condition for a fresh source — NewProjector treats it as "start
// from an empty Checkpoint" rather than propagating it as an error.
var ErrCheckpointNotFound = errors.New("eventprojection: checkpoint not found")

// defaultDedupRingSize bounds how many recently-committed upstream
// identities a Checkpoint remembers. Once exceeded, the oldest identity is
// evicted from both Committed and IdentitySeq. This is a deliberate,
// documented tradeoff: it bounds memory/storage for long-running sessions
// at the cost of losing dedup/replay-identity for identities older than the
// ring — acceptable because a real upstream cursor reappearing that far in
// the past is not expected in practice, and re-projecting such a stale
// identity as new is safe (never silently drops or corrupts history).
const defaultDedupRingSize = 256

// Checkpoint is the durable reconciliation state for one upstream SourceID.
type Checkpoint struct {
	Source SourceID
	// Epoch increments whenever the cursor space is known to have been
	// invalidated (see Projector.ResetEpoch). A fresh Epoch clears all
	// dedup/identity state, forcing the next committed event back into
	// PhaseSnapshot.
	Epoch int
	// LastCursor is the most recently observed non-empty UpstreamCursor.
	LastCursor string
	// Committed is a bounded, oldest-first ring of upstream identities that
	// have been observed (whether projected or resolved as a confirmed
	// local-prompt echo), used to bound IdentitySeq's size via eviction.
	Committed []string
	// IdentitySeq maps an observed upstream identity to the Mitto seq it
	// was committed under, or to -1 as a sentinel meaning "observed and
	// resolved as a confirmed local-prompt echo, never projected". Replay
	// of an identity present here reuses its recorded determination rather
	// than re-deciding (see Projector.commitDiscreteLocked).
	IdentitySeq map[string]int64
}

// newCheckpoint returns a fresh, empty Checkpoint for src.
func newCheckpoint(src SourceID) *Checkpoint {
	return &Checkpoint{Source: src, IdentitySeq: make(map[string]int64)}
}

// recordIdentity records identity -> seq (or the -1 echo sentinel),
// evicting the oldest entry from the dedup ring once defaultDedupRingSize
// is exceeded.
func (c *Checkpoint) recordIdentity(identity string, seq int64) {
	if c.IdentitySeq == nil {
		c.IdentitySeq = make(map[string]int64)
	}
	if _, exists := c.IdentitySeq[identity]; !exists {
		c.Committed = append(c.Committed, identity)
		if len(c.Committed) > defaultDedupRingSize {
			evicted := c.Committed[0]
			c.Committed = c.Committed[1:]
			delete(c.IdentitySeq, evicted)
		}
	}
	c.IdentitySeq[identity] = seq
}

// CheckpointStore loads and saves per-source checkpoints. The pure core
// package depends only on this interface; a durable, session-sidecar-backed
// implementation lives in the sibling package eventprojectionsession (kept
// outside this leaf so this package's import guard never has to allow
// internal/session — see doc.go and imports_test.go).
type CheckpointStore interface {
	// Load returns the persisted Checkpoint for src, or ErrCheckpointNotFound
	// if none has been persisted yet.
	Load(src SourceID) (*Checkpoint, error)
	// Save durably persists cp.
	Save(cp *Checkpoint) error
}
