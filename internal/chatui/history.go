package chatui

import (
	"encoding/json"

	"github.com/inercia/mitto/pkg/api"
)

// applySyncEvent decodes one api.SyncEvent (as returned by --history's
// Session.LoadEvents / OnEventsLoaded) into the transcript. SyncEvent.Data
// is an already-JSON-decoded interface{} (unlike the live Event stream's
// Raw json.RawMessage), so it is re-marshaled here to reuse the same
// per-type field shapes pkg/api/stream.go's eventFromMessage decodes from
// the live WebSocket — kept in sync manually since eventFromMessage itself
// is unexported and keyed on a different envelope type (wsMessage vs
// SyncEvent).
func applySyncEvent(t *transcript, ev api.SyncEvent) {
	raw, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}

	switch ev.Type {
	case "agent_message":
		var d struct {
			HTML string `json:"html"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &d) == nil {
			t.AppendOrUpdateAgent(ev.Seq, d.Text, d.HTML)
		}
	case "agent_thought":
		var d struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &d) == nil {
			t.AppendThought(d.Text)
		}
	case "tool_call":
		var d struct{ ID, Title, Status string }
		if json.Unmarshal(raw, &d) == nil {
			t.AppendTool(d.ID, d.Title, d.Status)
		}
	case "tool_update":
		var d struct{ ID, Status string }
		if json.Unmarshal(raw, &d) == nil {
			t.UpdateTool(d.ID, d.Status)
		}
	case "file_read", "file_write":
		var d struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		}
		if json.Unmarshal(raw, &d) == nil {
			verb := "read"
			if ev.Type == "file_write" {
				verb = "write"
			}
			t.AppendFileEvent(verb, d.Path, d.Size)
		}
	case "user_prompt":
		var d struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &d) == nil {
			t.AppendUser(d.Message)
		}
	case "error":
		var d struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &d) == nil {
			t.AppendError(d.Message)
		}
	}
}
