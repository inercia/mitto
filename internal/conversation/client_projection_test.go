package conversation

import (
	"context"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/eventprojection"
)

// TestTranslateACPUpdateToNeutral_Kinds covers each update kind this file
// routes through the projector, mirroring internal/acpbackend/events.go's
// translateSessionUpdate parity for the shared kinds.
func TestTranslateACPUpdateToNeutral_Kinds(t *testing.T) {
	t.Run("agent message chunk", func(t *testing.T) {
		ev, ok := translateACPUpdateToNeutral(acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "hi"}},
			},
		})
		if !ok || ev.Kind != agentbackend.EventAgentMessage || ev.Origin != agentbackend.OriginLocal {
			t.Fatalf("unexpected event: ok=%v ev=%+v", ok, ev)
		}
		if len(ev.Content) != 1 || ev.Content[0].Text == nil || ev.Content[0].Text.Text != "hi" {
			t.Fatalf("unexpected content: %+v", ev.Content)
		}
	})

	t.Run("agent thought chunk", func(t *testing.T) {
		ev, ok := translateACPUpdateToNeutral(acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "thinking"}},
			},
		})
		if !ok || ev.Kind != agentbackend.EventAgentThought {
			t.Fatalf("unexpected event: ok=%v ev=%+v", ok, ev)
		}
	})

	t.Run("tool call", func(t *testing.T) {
		ev, ok := translateACPUpdateToNeutral(acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "t1", Title: "Read", Status: acp.ToolCallStatusInProgress},
		})
		if !ok || ev.Kind != agentbackend.EventToolCall || ev.ToolCall == nil {
			t.Fatalf("unexpected event: ok=%v ev=%+v", ok, ev)
		}
		if ev.ToolCall.ID != "t1" || ev.ToolCall.Title != "Read" || ev.ToolCall.Update {
			t.Fatalf("unexpected tool call payload: %+v", ev.ToolCall)
		}
	})

	t.Run("tool call update", func(t *testing.T) {
		status := acp.ToolCallStatusCompleted
		ev, ok := translateACPUpdateToNeutral(acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "t1", Status: &status},
		})
		if !ok || ev.ToolCall == nil || !ev.ToolCall.Update || ev.ToolCall.Status != string(status) {
			t.Fatalf("unexpected tool call update payload: %+v", ev.ToolCall)
		}
	})

	t.Run("plan", func(t *testing.T) {
		ev, ok := translateACPUpdateToNeutral(acp.SessionUpdate{
			Plan: &acp.SessionUpdatePlan{Entries: []acp.PlanEntry{
				{Content: "A", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusPending},
			}},
		})
		if !ok || ev.Plan == nil || len(ev.Plan.Entries) != 1 || ev.Plan.Entries[0].Content != "A" {
			t.Fatalf("unexpected plan payload: %+v", ev.Plan)
		}
	})

	t.Run("unsupported kind", func(t *testing.T) {
		if _, ok := translateACPUpdateToNeutral(acp.SessionUpdate{}); ok {
			t.Fatal("expected ok=false for an update with no recognized kind")
		}
	})
}

// TestNewACPProjector_EmptyProviderSessionIsSafe proves construction never
// errors even when ProviderSession is unknown at WebClient-build time (the
// deferred-handshake case — see bgsession_shared_session.go).
func TestNewACPProjector_EmptyProviderSessionIsSafe(t *testing.T) {
	sb := NewStreamBuffer(StreamBufferConfig{})
	proj, err := newACPProjector(eventprojection.SourceID{Backend: "acp", Provider: "auggie"}, sb)
	if err != nil {
		t.Fatalf("newACPProjector: %v", err)
	}
	if proj == nil {
		t.Fatal("expected non-nil Projector")
	}
}

// recordedCalls captures every StreamBuffer-facing callback WebClient made,
// in order, so the projector-enabled and legacy paths can be compared for
// byte-identical observer output.
type recordedCalls struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordedCalls) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, s)
}

func newRecordingWebClient(t *testing.T, enableProjection bool) (*WebClient, *recordedCalls) {
	t.Helper()
	rec := &recordedCalls{}
	client := NewWebClient(WebClientConfig{
		EnableEventProjection: enableProjection,
		EventProjectionSource: eventprojection.SourceID{Backend: "acp", Provider: "test"},
		OnAgentMessage: func(seq int64, html, markdown string) {
			rec.add("message:" + markdown)
		},
		OnAgentThought: func(seq int64, text string) {
			rec.add("thought:" + text)
		},
		OnToolCall: func(seq int64, id, title, status string) {
			rec.add("toolcall:" + id + ":" + title + ":" + status)
		},
		OnToolUpdate: func(seq int64, id string, status *string) {
			s := ""
			if status != nil {
				s = *status
			}
			rec.add("toolupdate:" + id + ":" + s)
		},
		OnPlan: func(seq int64, entries []PlanEntry) {
			out := "plan:"
			for _, e := range entries {
				out += e.Content + "|"
			}
			rec.add(out)
		},
	})
	t.Cleanup(client.Close)
	return client, rec
}

// TestWebClient_ProjectorPassthrough_MatchesLegacyObserverOutput scripts a
// mixed SessionNotification stream (message chunk, thought, tool call, tool
// call update, plan) through a projector-enabled WebClient and a legacy
// (EnableEventProjection: false) one, asserting the observer callback
// sequences are byte-identical — the transparent-passthrough invariant this
// bead's plan requires (mitto-mx9.2).
func TestWebClient_ProjectorPassthrough_MatchesLegacyObserverOutput(t *testing.T) {
	script := func(client *WebClient) {
		ctx := context.Background()
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("SessionUpdate: %v", err)
			}
		}
		must(client.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "hello "}},
			},
		}}))
		client.FlushMarkdown()
		must(client.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "pondering"}},
			},
		}}))
		must(client.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "t1", Title: "Read", Status: acp.ToolCallStatusInProgress},
		}}))
		status := acp.ToolCallStatusCompleted
		must(client.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "t1", Status: &status},
		}}))
		must(client.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{
			Plan: &acp.SessionUpdatePlan{Entries: []acp.PlanEntry{
				{Content: "Step 1", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusInProgress},
			}},
		}}))
		client.FlushMarkdown()
	}

	legacyClient, legacyRec := newRecordingWebClient(t, false)
	script(legacyClient)

	projClient, projRec := newRecordingWebClient(t, true)
	script(projClient)

	if projClient.projector == nil {
		t.Fatal("expected projector to be wired when EnableEventProjection is true")
	}

	legacyRec.mu.Lock()
	projRec.mu.Lock()
	defer legacyRec.mu.Unlock()
	defer projRec.mu.Unlock()

	if len(legacyRec.lines) == 0 {
		t.Fatal("expected at least one recorded callback")
	}
	if len(legacyRec.lines) != len(projRec.lines) {
		t.Fatalf("callback count mismatch: legacy=%v proj=%v", legacyRec.lines, projRec.lines)
	}
	for i := range legacyRec.lines {
		if legacyRec.lines[i] != projRec.lines[i] {
			t.Errorf("callback[%d] mismatch: legacy=%q proj=%q", i, legacyRec.lines[i], projRec.lines[i])
		}
	}
}

// TestWebClient_MittoToolCallCorrelation_UnaffectedByProjection guards the
// subtle invariant documented in client.go's SessionUpdate: onMittoToolCall
// correlation is extracted from the raw ACP acp.SessionUpdateToolCall.RawInput
// BEFORE dispatchViaProjector runs, and the neutral agentbackend.ToolCallPayload
// translateACPUpdateToNeutral produces has no RawInput field at all. If a
// future refactor ever moved the mitto_* correlation check to read from the
// neutral event instead of the raw ACP update, or reordered it to run after
// projection dispatch, self_id correlation for mitto_* tool calls would
// silently break only when EnableEventProjection is true. This test pins
// both branches (correlation ID present vs. RawInput-less fallback) with
// projection enabled, matching the legacy-path assertions already covered
// by TestWebClient_SessionUpdate_ToolCall and
// TestWebClient_MittoToolCallWithoutRawInputUsesSafeFallback.
func TestWebClient_MittoToolCallCorrelation_UnaffectedByProjection(t *testing.T) {
	t.Run("self_id extracted from RawInput", func(t *testing.T) {
		var correlationID string
		var toolID, toolTitle, toolStatus string
		client := NewWebClient(WebClientConfig{
			EnableEventProjection: true,
			EventProjectionSource: eventprojection.SourceID{Backend: "acp", Provider: "test"},
			OnMittoToolCall:       func(id string) { correlationID = id },
			OnToolCall: func(seq int64, id, title, status string) {
				toolID, toolTitle, toolStatus = id, title, status
			},
		})
		defer client.Close()

		if client.projector == nil {
			t.Fatal("expected projector to be wired when EnableEventProjection is true")
		}

		err := client.SessionUpdate(context.Background(), acp.SessionNotification{
			Update: acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: "tool-mitto",
				Title:      "mitto_conversation_get_current",
				Status:     acp.ToolCallStatusInProgress,
				RawInput:   map[string]any{"self_id": "sess-abc"},
			}},
		})
		if err != nil {
			t.Fatalf("SessionUpdate failed: %v", err)
		}
		if correlationID != "sess-abc" {
			t.Fatalf("correlationID = %q, want %q (projection must not interfere with raw-ACP RawInput correlation)", correlationID, "sess-abc")
		}
		// The tool call must still reach the observer via the projector seam.
		if toolID != "tool-mitto" || toolTitle != "mitto_conversation_get_current" || toolStatus != string(acp.ToolCallStatusInProgress) {
			t.Fatalf("tool call not delivered via projector: id=%q title=%q status=%q", toolID, toolTitle, toolStatus)
		}
	})

	t.Run("RawInput-less fallback preserved", func(t *testing.T) {
		var correlationID string
		client := NewWebClient(WebClientConfig{
			EnableEventProjection: true,
			EventProjectionSource: eventprojection.SourceID{Backend: "acp", Provider: "test"},
			OnMittoToolCall:       func(id string) { correlationID = id },
		})
		defer client.Close()

		err := client.SessionUpdate(context.Background(), acp.SessionNotification{
			Update: acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: "tool-mitto-2",
				Title:      "mitto_conversation_get_current",
				Status:     acp.ToolCallStatusInProgress,
			}},
		})
		if err != nil {
			t.Fatalf("SessionUpdate failed: %v", err)
		}
		if correlationID != "" {
			t.Fatalf("RawInput-less tool call used ambiguous correlation %q, want callback-owned fallback", correlationID)
		}
	})
}
