package mcpserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChildReportHistory_LegacyMapping(t *testing.T) {
	const raw = `{
		"current_task_id":"do-not-infer",
		"unknown_state":{"keep":"in snapshot"},
		"reports":{
			"scoped":{"report":{"status":"PROGRESS / 未完","summary":"Résumé 🦉","details":"","unknown":{"n":1}},"completed":true,"timestamp":"2026-10-26T12:00:00Z","task_id":"task-A"},
			"unscoped":{"report":{"status":"stuck","summary":"client evidence"},"completed":true,"timestamp":"2026-10-26T12:00:01Z"},
			"mismatch":{"report":{"status":"success","summary":"other task"},"completed":true,"timestamp":"2026-10-26T12:00:02Z","task_id":"task-old","task_mismatch":true},
			"unscoped-mismatch":{"report":{"status":"success","summary":"no identity"},"completed":true,"timestamp":"2026-10-26T12:00:03Z","task_mismatch":true},
			"idle":{"report":{"status":"auto_completed","summary":"idle"},"completed":true,"timestamp":"2026-10-26T12:00:04Z","auto_completed":true,"auto_reason":"agent_idle","unknown_slot":{"keep":true}},
			"failed":{"report":{"status":"failed","summary":"send failed"},"completed":true,"timestamp":"2026-10-26T12:00:05Z","failed":true,"fail_message":"queue error","task_id":"context-only"},
			"mixed":{"report":{"status":"success","summary":"ambiguous newer payload","details":"preserve me"},"completed":true,"timestamp":"2026-10-26T12:00:06Z","auto_completed":true,"failed":true,"task_mismatch":true,"auto_reason":"old idle","fail_message":"old failure","future_flag":17},
			"pending":null
		}
	}`
	state, err := decodeChildReportHistory("parent", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || state.CurrentTaskID != "do-not-infer" {
		t.Fatalf("unexpected state context: %+v", state)
	}
	if len(state.Entries) != 4 || len(state.Diagnostics) != 5 {
		t.Fatalf("got %d entries and %d diagnostics", len(state.Entries), len(state.Diagnostics))
	}
	wantChildren := []string{"scoped", "unscoped", "mismatch", "unscoped-mismatch"}
	for i, childID := range wantChildren {
		entry := state.Entries[i]
		if entry.ChildID != childID {
			t.Fatalf("entry %d child = %q, want %q", i, entry.ChildID, childID)
		}
		if _, err := uuid.Parse(entry.ID); err != nil {
			t.Fatal(err)
		}
		if entry.ReceivedAt != time.Date(2026, 10, 26, 12, 0, i, 0, time.UTC) {
			t.Errorf("timestamp changed: %s", entry.ReceivedAt)
		}
		if entry.SuppliedTaskID == "" {
			if entry.EffectiveTaskID != "" || entry.Association != childReportUnassociated {
				t.Errorf("legacy unscoped report was associated: %+v", entry)
			}
		} else if entry.EffectiveTaskID != entry.SuppliedTaskID || entry.Association != childReportExplicit {
			t.Errorf("explicit identity changed: %+v", entry)
		}
	}
	if state.Entries[0].SuppliedTaskID != "task-A" || state.Entries[2].SuppliedTaskID != "task-old" {
		t.Fatal("supplied task IDs changed")
	}
	var legacy legacyChildReportState
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		t.Fatal(err)
	}
	for _, entry := range state.Entries {
		var slot childReport
		if err := json.Unmarshal(legacy.Reports[entry.ChildID], &slot); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(entry.Report, slot.Report) {
			t.Errorf("payload bytes changed for %s", entry.ChildID)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(state.Entries[0].Report, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "PROGRESS / 未完" || payload["summary"] != "Résumé 🦉" || payload["details"] != "" || payload["unknown"] == nil {
		t.Fatalf("payload changed: %+v", payload)
	}
	wantKinds := map[string]childWaitDiagnosticKind{
		"idle": childDiagnosticAutoCompleted, "failed": childDiagnosticFailed,
		"mixed": childDiagnosticLegacyMixed, "mismatch": childDiagnosticTaskMismatch,
		"unscoped-mismatch": childDiagnosticTaskMismatch,
	}
	for childID, kind := range wantKinds {
		diagnostic := state.Diagnostics[childID]
		if diagnostic.Kind != kind || !bytes.Equal(diagnostic.Legacy, legacy.Reports[childID]) {
			t.Errorf("legacy diagnostic lost data: %+v", diagnostic)
		}
	}
	if state.Diagnostics["idle"].Reason != "agent_idle" || state.Diagnostics["failed"].Message != "queue error" || state.Diagnostics["failed"].TaskID != "context-only" {
		t.Fatal("diagnostic context changed")
	}
	// Conversion must never mutate input buffers/maps, and repeated conversion
	// of the same snapshot has stable IDs despite map iteration order.
	again, err := decodeChildReportHistory("parent", json.RawMessage(raw))
	if err != nil || !reflect.DeepEqual(state, again) {
		t.Fatalf("conversion not deterministic: %v", err)
	}
}

func TestChildReportHistory_LegacyOrderAndIdentity(t *testing.T) {
	raw := json.RawMessage(`{"reports":{
		"z":{"report":{"status":"success","summary":"same"},"completed":true,"timestamp":"2026-10-26T12:00:00Z"},
		"a":{"report":{"status":"success","summary":"same"},"completed":true,"timestamp":"2026-10-26T12:00:00Z"},
		"first":{"report":{"status":"success","summary":"same"},"completed":true,"timestamp":"2026-10-25T12:00:00Z"}
	}}`)
	state, err := decodeChildReportHistory("parent", raw)
	if err != nil {
		t.Fatal(err)
	}
	for i, childID := range []string{"first", "a", "z"} {
		if state.Entries[i].ChildID != childID {
			t.Fatalf("migration order: %+v", state.Entries)
		}
	}
	if state.Entries[1].ID == state.Entries[2].ID {
		t.Fatal("identical text from distinct children shared an ID")
	}
	otherParent, err := decodeChildReportHistory("other-parent", raw)
	if err != nil || otherParent.Entries[0].ID == state.Entries[0].ID {
		t.Fatalf("parent not part of migrated identity: %v", err)
	}
	// The conversion owns its result, including diagnostic and report bytes.
	before := bytes.Clone(state.Entries[0].Report)
	for i := range raw {
		raw[i] = ' '
	}
	if !bytes.Equal(state.Entries[0].Report, before) {
		t.Fatal("history aliases conversion input")
	}
}

func TestChildReportHistory_LegacyOriginAndOwnership(t *testing.T) {
	for _, status := range []string{"auto_completed", "failed", "stuck", "task_mismatch"} {
		t.Run(status, func(t *testing.T) {
			payload, err := json.Marshal(map[string]string{"status": status, "summary": "genuine client evidence"})
			if err != nil {
				t.Fatal(err)
			}
			slot, err := json.Marshal(childReport{Report: payload, Completed: true, Timestamp: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			state, err := convertLegacyChildReports("parent", legacyChildReportState{Reports: map[string]json.RawMessage{"child": slot}})
			if err != nil || len(state.Entries) != 1 || len(state.Diagnostics) != 0 {
				t.Fatalf("status text misclassified origin: %+v, %v", state, err)
			}
		})
	}
	raw := json.RawMessage(`{"report":{"status":"success","summary":"ambiguous evidence","extra":true},"auto_completed":true,"completed":true,"timestamp":"2026-10-26T12:00:00Z","unknown":17}`)
	legacy := legacyChildReportState{Reports: map[string]json.RawMessage{"child": raw}}
	state, err := convertLegacyChildReports("parent", legacy)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(raw)
	for i := range raw {
		raw[i] = ' '
	}
	delete(legacy.Reports, "child")
	if len(state.Entries) != 0 || !bytes.Equal(state.Diagnostics["child"].Legacy, before) {
		t.Fatal("diagnostic lost ambiguous data or aliases legacy input")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := decodeChildReportHistory("parent", encoded)
	if err != nil || !reflect.DeepEqual(state, reloaded) {
		t.Fatalf("diagnostic round trip lost data: %v", err)
	}
}

func TestChildReportHistory_OwnershipAndProjection(t *testing.T) {
	raw := json.RawMessage(`{"status":"failed","summary":"genuine","details":"","extra":[1,2]}`)
	entry, err := newChildReportEntry(uuid.NewString(), "child", time.Now(), "", raw, "task", childReportInferred)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(entry.Report)
	raw[0] = ' '
	if !bytes.Equal(entry.Report, before) {
		t.Fatal("entry aliases caller payload")
	}
	view := entry.compatibilityReport()
	if !view.Completed || view.AutoCompleted || view.Failed || view.TaskMismatch || view.AutoReason != "" || view.FailMessage != "" {
		t.Fatalf("genuine evidence inherited diagnostic flags: %+v", view)
	}
	if view.TaskID != "" || entry.EffectiveTaskID != "task" || !view.Timestamp.Equal(entry.ReceivedAt) {
		t.Fatal("projection changed identity/time or promoted inferred task to supplied")
	}
	view.Report[0] = ' '
	if !bytes.Equal(entry.Report, before) {
		t.Fatal("compatibility view aliases immutable entry")
	}
	if !bytes.Equal(entry.compatibilityReport().Report, before) {
		t.Fatal("later projection was affected by mutable view")
	}
}

func TestChildReportHistory_VersionOneRoundTripPreservesOrder(t *testing.T) {
	state := childReportHistoryState{Version: 1, Entries: []childReportEntry{}, Diagnostics: map[string]childWaitDiagnostic{}}
	// Arrival order intentionally opposes the clock; duplicate text is evidence,
	// not a dedupe key. Both IDs and stored order must survive decoding.
	for i, association := range []childReportAssociation{childReportExplicit, childReportInferred, childReportUnassociated} {
		supplied, effective := "", ""
		if association == childReportExplicit {
			supplied, effective = "task", "task"
		} else if association == childReportInferred {
			effective = "task"
		}
		entry, err := newChildReportEntry(uuid.NewString(), "child", time.Date(2026, 10, 26, 12, 0, 3-i, 0, time.UTC), supplied,
			json.RawMessage(`{"status":"unknown","summary":"identical","details":""}`), effective, association)
		if err != nil {
			t.Fatal(err)
		}
		state.Entries = append(state.Entries, entry)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChildReportHistory("parent", raw)
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("round trip changed ordered evidence: %v\ngot: %+v\nwant: %+v", err, got, state)
	}
	for i := range raw {
		raw[i] = ' '
	}
	if !bytes.Equal(got.Entries[0].Report, state.Entries[0].Report) {
		t.Fatal("decoded payload aliases input")
	}
}

func TestChildReportHistory_InvalidState(t *testing.T) {
	for name, raw := range map[string]string{
		"corrupt": `{`, "null": `null`, "array": `[]`, "missing shape": `{}`,
		"future":           `{"version":2,"entries":[],"diagnostics":{}}`,
		"negative version": `{"version":-1}`, "string version": `{"version":"1"}`, "null version": `{"version":null}`,
		"missing entries": `{"version":1,"diagnostics":{}}`, "null entries": `{"version":1,"entries":null,"diagnostics":{}}`,
		"missing diagnostics": `{"version":1,"entries":[]}`, "null diagnostics": `{"version":1,"entries":[],"diagnostics":null}`,
		"wrong entries shape":     `{"version":1,"entries":{},"diagnostics":{}}`,
		"wrong diagnostics shape": `{"version":1,"entries":[],"diagnostics":[]}`,
		"legacy null map":         `{"reports":null}`, "legacy array": `{"reports":[]}`,
		"mixed schema":                `{"version":1,"entries":[],"diagnostics":{},"reports":{}}`,
		"unversioned history":         `{"reports":{},"entries":[]}`,
		"unversioned diagnostics":     `{"reports":{},"diagnostics":{}}`,
		"malformed slot":              `{"reports":{"child":[]}}`,
		"empty child":                 `{"reports":{"":null}}`,
		"incomplete slot":             `{"reports":{"child":{"report":{"status":"ok","summary":"evidence"},"timestamp":"2026-10-26T12:00:00Z"}}}`,
		"synthetic malformed payload": `{"reports":{"child":{"auto_completed":true,"report":null,"timestamp":"2026-10-26T12:00:00Z"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeChildReportHistory("parent", json.RawMessage(raw)); err == nil {
				t.Fatalf("accepted invalid state: %s", raw)
			}
		})
	}
}

func TestChildReportHistory_InvalidEntry(t *testing.T) {
	valid, err := newChildReportEntry(uuid.NewString(), "child", time.Now(), "task", json.RawMessage(`{"status":"ok","summary":"evidence"}`), "task", childReportExplicit)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*childReportEntry){
		"missing ID":               func(e *childReportEntry) { e.ID = "" },
		"bad ID":                   func(e *childReportEntry) { e.ID = "not-a-uuid" },
		"nil UUID":                 func(e *childReportEntry) { e.ID = uuid.Nil.String() },
		"missing child":            func(e *childReportEntry) { e.ChildID = "" },
		"missing timestamp":        func(e *childReportEntry) { e.ReceivedAt = time.Time{} },
		"unknown association":      func(e *childReportEntry) { e.Association = "guessed" },
		"explicit mismatch":        func(e *childReportEntry) { e.EffectiveTaskID = "other" },
		"explicit empty supplied":  func(e *childReportEntry) { e.SuppliedTaskID = "" },
		"explicit empty effective": func(e *childReportEntry) { e.EffectiveTaskID = "" },
		"inferred supplied":        func(e *childReportEntry) { e.Association = childReportInferred },
		"inferred missing effective": func(e *childReportEntry) {
			e.Association = childReportInferred
			e.SuppliedTaskID = ""
			e.EffectiveTaskID = ""
		},
		"unassociated supplied":  func(e *childReportEntry) { e.Association = childReportUnassociated; e.EffectiveTaskID = "" },
		"unassociated effective": func(e *childReportEntry) { e.Association = childReportUnassociated; e.SuppliedTaskID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			entry := valid
			mutate(&entry)
			if err := validateChildReportEntry(entry); err == nil {
				t.Fatalf("accepted invalid entry: %+v", entry)
			}
		})
	}
	for _, payload := range []string{`null`, `[]`, `{}`, `{"status":null,"summary":"x"}`, `{"status":17,"summary":"x"}`, `{"status":"","summary":"x"}`, `{"status":"ok","summary":""}`, `{"status":"ok","summary":"x","details":false}`} {
		entry := valid
		entry.Report = json.RawMessage(payload)
		if err := validateChildReportEntry(entry); err == nil {
			t.Errorf("accepted invalid payload: %s", payload)
		}
	}
	state := childReportHistoryState{Version: 1, Entries: []childReportEntry{valid, valid}, Diagnostics: map[string]childWaitDiagnostic{}}
	if err := validateChildReportHistory(state); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate ID not rejected: %v", err)
	}
	state.Entries[1].ID = strings.ToUpper(valid.ID)
	if err := validateChildReportHistory(state); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("alternate UUID spelling not rejected: %v", err)
	}
}

func TestChildReportHistory_InvalidDiagnostic(t *testing.T) {
	valid := childWaitDiagnostic{ChildID: "child", Kind: childDiagnosticFailed, ObservedAt: time.Now()}
	for name, mutate := range map[string]func(*childWaitDiagnostic){
		"child mismatch": func(d *childWaitDiagnostic) { d.ChildID = "other" },
		"missing time":   func(d *childWaitDiagnostic) { d.ObservedAt = time.Time{} },
		"unknown kind":   func(d *childWaitDiagnostic) { d.Kind = "unknown" },
		"null raw slot":  func(d *childWaitDiagnostic) { d.Legacy = json.RawMessage(`null`) },
		"array raw slot": func(d *childWaitDiagnostic) { d.Legacy = json.RawMessage(`[]`) },
	} {
		t.Run(name, func(t *testing.T) {
			diagnostic := valid
			mutate(&diagnostic)
			state := childReportHistoryState{Version: 1, Entries: []childReportEntry{}, Diagnostics: map[string]childWaitDiagnostic{"child": diagnostic}}
			if err := validateChildReportHistory(state); err == nil {
				t.Fatalf("accepted invalid diagnostic: %+v", diagnostic)
			}
		})
	}
}
