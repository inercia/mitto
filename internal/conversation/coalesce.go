// Package conversation — target.reuseCoalesce coalescing helper (mitto-djs1).
//
// PromptMatchesActiveOrQueued reports whether a candidate dispatch (identified
// by prompt name + argument map) is already in flight on bs or already queued.
// It is intended to be called INSIDE the per-key reuse lock
// (lockReuseIssue / lockReuseTitle / lockSingleton) so the compare-then-
// skip-or-enqueue sequence is atomic against concurrent duplicate dispatches.
//
// Match rule (open-question resolution recorded in the mitto-djs1 plan):
//
//   - "same prompt" == same PromptName AND same Arguments map (deep equal,
//     treating nil and empty maps as equivalent).
//   - Free-text dispatches (empty promptName) NEVER coalesce — they always
//     fall through to the normal enqueue path (a user typing the same text
//     twice is not a shortcut misclick).
//   - The queue is inspected via q.List() and every entry with a non-empty
//     PromptName is compared. Legacy free-text queued entries are ignored.
//   - The currently-executing dispatch is inspected via bs.ActivePromptDispatch()
//     (may be nil when bs has not been loaded yet — best-effort in that case).
//
// The caller is responsible for holding the appropriate lock; this helper is
// pure and side-effect free.
package conversation

import (
	"github.com/inercia/mitto/internal/session"
)

// bgSessionLike lets tests substitute the ActivePromptDispatch getter without
// having to construct a full BackgroundSession. Kept unexported — the only
// concrete implementation in production is *BackgroundSession.
type bgSessionLike interface {
	ActivePromptDispatch() (name string, args map[string]string, ok bool)
}

// queueLike lets tests substitute the queue List method. In production the
// only implementation is *session.Queue.
type queueLike interface {
	List() ([]session.QueuedMessage, error)
}

// PromptMatchesActiveOrQueued reports whether (promptName, arguments) matches
// the in-flight dispatch on bs OR any queued entry on q. Returns false when
// promptName is empty (free-text bypass), when both bs and q lack a match, or
// when q.List() returns an error (best-effort — a queue read failure must
// NOT silently coalesce a dispatch that would otherwise be delivered).
func PromptMatchesActiveOrQueued(bs bgSessionLike, q queueLike, promptName string, arguments map[string]string) bool {
	if promptName == "" {
		return false
	}
	// Compare against the currently-executing dispatch.
	if bs != nil {
		if activeName, activeArgs, ok := bs.ActivePromptDispatch(); ok {
			if activeName == promptName && argsDeepEqual(activeArgs, arguments) {
				return true
			}
		}
	}
	// Compare against every non-empty-name queued entry.
	if q != nil {
		msgs, err := q.List()
		if err != nil {
			return false
		}
		for i := range msgs {
			if msgs[i].PromptName == "" {
				continue
			}
			if msgs[i].PromptName == promptName && argsDeepEqual(msgs[i].Arguments, arguments) {
				return true
			}
		}
	}
	return false
}

// argsDeepEqual compares two argument maps for equality treating a nil map
// and an empty (len 0) map as equivalent. Two dispatches carrying no
// arguments must always coalesce regardless of whether one caller passed nil
// and the other passed {} through the JSON pipeline.
func argsDeepEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	// len(a) == len(b); if both zero the maps are equal regardless of nil-ness.
	for k, va := range a {
		vb, ok := b[k]
		if !ok || vb != va {
			return false
		}
	}
	return true
}
