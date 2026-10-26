package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/inercia/mitto/internal/session"
)

const childReportsStateFile = "child-reports.json"

type persistedChildReportCollector struct {
	CurrentTaskID string                  `json:"current_task_id,omitempty"`
	Reports       map[string]*childReport `json:"reports"`
}

// These DTOs and pure helpers define the next storage format only. The live
// loader/writer below intentionally still uses persistedChildReportCollector
// until recoverable migration and checked writes are implemented together.
const childReportHistoryVersion = 1

type childReportHistoryState struct {
	Version       int                            `json:"version"`
	CurrentTaskID string                         `json:"current_task_id,omitempty"`
	Entries       []childReportEntry             `json:"entries"`
	Diagnostics   map[string]childWaitDiagnostic `json:"diagnostics"`
}

type legacyChildReportState struct {
	CurrentTaskID string                     `json:"current_task_id,omitempty"`
	Reports       map[string]json.RawMessage `json:"reports"`
}

// newChildReportEntry takes ownership by copying, never by retaining caller
// bytes. Callers supply the already-decided association; this helper must not
// consult wait state or infer from timestamps (especially during migration).
func newChildReportEntry(id, childID string, receivedAt time.Time, suppliedTaskID string, report json.RawMessage, effectiveTaskID string, association childReportAssociation) (childReportEntry, error) {
	entry := childReportEntry{
		ID: id, ChildID: childID, ReceivedAt: receivedAt,
		SuppliedTaskID: suppliedTaskID, Report: bytes.Clone(report),
		EffectiveTaskID: effectiveTaskID, Association: association,
	}
	return entry, validateChildReportEntry(entry)
}

func validateChildReportPayload(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("report must be a JSON object")
	}
	for _, name := range []string{"status", "summary", "details"} {
		value, exists := fields[name]
		if !exists && name == "details" {
			continue
		}
		var text string
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil || (name != "details" && text == "") {
			return fmt.Errorf("report %s must be a string (status and summary must be nonempty)", name)
		}
	}
	return nil
}

func validateChildReportEntry(entry childReportEntry) error {
	if _, err := uuid.Parse(entry.ID); err != nil {
		return fmt.Errorf("invalid report entry ID %q: %w", entry.ID, err)
	}
	if uuid.MustParse(entry.ID) == uuid.Nil {
		return errors.New("report entry ID must not be nil UUID")
	}
	if entry.ChildID == "" || entry.ReceivedAt.IsZero() {
		return errors.New("report entry requires child_id and received_at")
	}
	switch entry.Association {
	case childReportExplicit:
		if entry.SuppliedTaskID == "" || entry.EffectiveTaskID != entry.SuppliedTaskID {
			return errors.New("explicit association requires matching nonempty supplied/effective task IDs")
		}
	case childReportInferred:
		if entry.SuppliedTaskID != "" || entry.EffectiveTaskID == "" {
			return errors.New("inferred association requires empty supplied and nonempty effective task IDs")
		}
	case childReportUnassociated:
		if entry.SuppliedTaskID != "" || entry.EffectiveTaskID != "" {
			return errors.New("unassociated report must have empty supplied/effective task IDs")
		}
	default:
		return fmt.Errorf("invalid report association %q", entry.Association)
	}
	return validateChildReportPayload(entry.Report)
}

func validateChildReportHistory(state childReportHistoryState) error {
	if state.Version != childReportHistoryVersion {
		return fmt.Errorf("unsupported child report history version %d", state.Version)
	}
	if state.Entries == nil || state.Diagnostics == nil {
		return errors.New("child report history requires entries array and diagnostics object")
	}
	seen := make(map[string]bool, len(state.Entries))
	for _, entry := range state.Entries {
		if err := validateChildReportEntry(entry); err != nil {
			return fmt.Errorf("entry %q: %w", entry.ID, err)
		}
		// Compare parsed UUIDs so alternate textual spellings cannot alias IDs.
		id := uuid.MustParse(entry.ID).String()
		if seen[id] {
			return fmt.Errorf("duplicate report entry ID %q", entry.ID)
		}
		seen[id] = true
	}
	for childID, diagnostic := range state.Diagnostics {
		if childID == "" || diagnostic.ChildID != childID || diagnostic.ObservedAt.IsZero() {
			return fmt.Errorf("invalid diagnostic context for child %q", childID)
		}
		switch diagnostic.Kind {
		case childDiagnosticAutoCompleted, childDiagnosticFailed, childDiagnosticStuck, childDiagnosticTaskMismatch, childDiagnosticLegacyMixed:
		default:
			return fmt.Errorf("invalid diagnostic kind %q", diagnostic.Kind)
		}
		if len(diagnostic.Legacy) != 0 {
			var slot map[string]json.RawMessage
			if err := json.Unmarshal(diagnostic.Legacy, &slot); err != nil || slot == nil {
				return fmt.Errorf("invalid legacy diagnostic for child %q", childID)
			}
		}
	}
	return nil
}

// decodeChildReportHistory does no I/O and never publishes a collector. Legacy
// conversion is not a migration commit: callers must preserve the original JSON
// snapshot and durably write the candidate before using it as accepted state.
func decodeChildReportHistory(parentID string, raw json.RawMessage) (childReportHistoryState, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || shape == nil {
		return childReportHistoryState{}, errors.New("child report state must be a JSON object")
	}
	version := 0
	if value, exists := shape["version"]; exists {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &version) != nil {
			return childReportHistoryState{}, errors.New("child report version must be an integer")
		}
	}
	switch version {
	case 0:
		if _, exists := shape["entries"]; exists {
			return childReportHistoryState{}, errors.New("legacy child report state cannot contain entries")
		}
		if _, exists := shape["diagnostics"]; exists {
			return childReportHistoryState{}, errors.New("legacy child report state cannot contain diagnostics")
		}
		var legacy legacyChildReportState
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return childReportHistoryState{}, fmt.Errorf("invalid legacy child report state: %w", err)
		}
		return convertLegacyChildReports(parentID, legacy)
	case childReportHistoryVersion:
		if _, exists := shape["reports"]; exists {
			return childReportHistoryState{}, errors.New("child report history cannot contain legacy reports")
		}
		var state childReportHistoryState
		if err := json.Unmarshal(raw, &state); err != nil {
			return childReportHistoryState{}, fmt.Errorf("invalid child report history: %w", err)
		}
		return state, validateChildReportHistory(state)
	default:
		return childReportHistoryState{}, fmt.Errorf("unsupported child report history version %d", version)
	}
}

func convertLegacyChildReports(parentID string, legacy legacyChildReportState) (childReportHistoryState, error) {
	state := childReportHistoryState{
		Version: childReportHistoryVersion, CurrentTaskID: legacy.CurrentTaskID,
		Entries: []childReportEntry{}, Diagnostics: make(map[string]childWaitDiagnostic),
	}
	if parentID == "" || legacy.Reports == nil {
		return state, errors.New("legacy conversion requires parent ID and reports object")
	}
	for childID, raw := range legacy.Reports {
		if childID == "" {
			return state, errors.New("legacy report requires child ID")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue // Pending slot, not evidence.
		}
		var slot childReport
		if err := json.Unmarshal(raw, &slot); err != nil {
			return state, fmt.Errorf("invalid legacy report for child %q: %w", childID, err)
		}
		if err := validateChildReportPayload(slot.Report); err != nil {
			return state, fmt.Errorf("invalid legacy payload for child %q: %w", childID, err)
		}
		// Origin flags, never payload status text, identify synthetic/ambiguous
		// slots. Preserve all original data rather than guessing genuine origin.
		if slot.AutoCompleted || slot.Failed || slot.TaskMismatch {
			kind := childDiagnosticTaskMismatch
			switch {
			case slot.AutoCompleted && slot.Failed:
				kind = childDiagnosticLegacyMixed
			case slot.AutoCompleted:
				kind = childDiagnosticAutoCompleted
			case slot.Failed:
				kind = childDiagnosticFailed
			}
			state.Diagnostics[childID] = childWaitDiagnostic{
				ChildID: childID, Kind: kind, Reason: slot.AutoReason, Message: slot.FailMessage,
				ObservedAt: slot.Timestamp, TaskID: slot.TaskID, Legacy: bytes.Clone(raw),
			}
		}
		if slot.AutoCompleted || slot.Failed {
			continue
		}
		if !slot.Completed {
			return state, fmt.Errorf("legacy non-null report for child %q is not completed", childID)
		}
		association := childReportUnassociated
		if slot.TaskID != "" {
			association = childReportExplicit
		}
		// Frame parent, child and original slot identity unambiguously as JSON.
		// Retry of the same snapshot never generates random new IDs. Payload
		// values remain untouched; this is not text-based deduplication.
		identity, err := json.Marshal([]any{parentID, childID, slot})
		if err != nil {
			return state, fmt.Errorf("legacy report identity: %w", err)
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, identity).String()
		entry, err := newChildReportEntry(id, childID, slot.Timestamp, slot.TaskID, slot.Report, slot.TaskID, association)
		if err != nil {
			return state, fmt.Errorf("legacy report for child %q: %w", childID, err)
		}
		state.Entries = append(state.Entries, entry)
	}
	// Legacy maps have no arrival sequence. This deterministic order is not a
	// claim to have recovered history. Version-1 decoding never re-sorts entries.
	sort.Slice(state.Entries, func(i, j int) bool {
		a, b := state.Entries[i], state.Entries[j]
		if a.ReceivedAt.Equal(b.ReceivedAt) {
			return a.ChildID < b.ChildID
		}
		return a.ReceivedAt.Before(b.ReceivedAt)
	})
	return state, validateChildReportHistory(state)
}

// compatibilityReport returns a fresh flag-free view and independent payload.
// It deliberately makes no current-task selection or status-terminality choice.
func (entry childReportEntry) compatibilityReport() *childReport {
	return &childReport{
		Report: bytes.Clone(entry.Report), Completed: true,
		Timestamp: entry.ReceivedAt, TaskID: entry.SuppliedTaskID,
	}
}

func loadChildReportCollector(store *session.Store, parentSessionID string) (*childReportCollector, error) {
	collector := &childReportCollector{
		parentSessionID: parentSessionID,
		reports:         make(map[string]*childReport),
		store:           store,
	}
	if store == nil {
		return collector, nil
	}
	var state persistedChildReportCollector
	err := store.ReadSessionSidecarJSON(parentSessionID, childReportsStateFile, &state)
	if errors.Is(err, os.ErrNotExist) {
		return collector, nil
	}
	if err != nil {
		return nil, err
	}
	collector.currentTaskID = state.CurrentTaskID
	if state.Reports != nil {
		collector.reports = state.Reports
	}
	return collector, nil
}

func (c *childReportCollector) persist() error {
	if c.store == nil {
		return nil
	}
	// Lock order is collector.mu before Store locks. Store code never calls back
	// into a collector, so persistence cannot invert this order.
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.WriteSessionSidecarJSON(c.parentSessionID, childReportsStateFile, persistedChildReportCollector{
		CurrentTaskID: c.currentTaskID,
		Reports:       c.reports,
	})
}

func (s *Server) deleteChildReportCollector(parentSessionID string) {
	s.childReportCollectorsMu.Lock()
	delete(s.childReportCollectors, parentSessionID)
	s.childReportCollectorsMu.Unlock()
}
