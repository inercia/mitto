package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func historyPtr[T any](v T) *T { return &v }

func historyAdd(t *testing.T, c *childReportCollector, child, task, raw string) childReportEntry {
	t.Helper()
	if _, err := c.addReport(child, task, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[len(c.entries)-1]
}

func historyRead(t *testing.T, s *Server, input ChildrenTasksHistoryInput) ChildrenTasksHistoryOutput {
	t.Helper()
	_, out, err := s.handleChildrenTasksHistory(t.Context(), nil, input)
	if err != nil || !out.Success || out.Entries == nil {
		t.Fatalf("history failed: %+v err=%v", out, err)
	}
	return out
}

func TestChildrenTasksHistory_FiltersPagesAndReload(t *testing.T) {
	s, store, parent, children := setupParentChildSessions(t, 2)
	c, err := s.getOrCreateCollector(parent)
	if err != nil {
		t.Fatal(err)
	}
	large := `{"status":"failed","summary":"` + strings.Repeat("x", 3000) + `","unknown":9007199254740993,"nested":{"keep":true}}`
	entries := []childReportEntry{historyAdd(t, c, children[0], "A", large)}
	owner, _ := c.reserveWait()
	if _, err := c.startWait(owner, "A", children, false); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, historyAdd(t, c, children[1], "", `{"status":"completed","summary":"inferred"}`))
	c.finalizeWait(owner, true)
	c.clearWait(owner)
	entries = append(entries, historyAdd(t, c, children[0], "", `{"status":"completed","summary":"unassociated"}`))
	for i := 3; i < 8; i++ {
		entries = append(entries, historyAdd(t, c, children[0], "B", fmt.Sprintf(`{"status":"in_progress","summary":"%d"}`, i)))
	}
	first := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent})
	if len(first.Entries) != 5 || !first.HasMore || first.NextBeforeReportID != entries[3].ID {
		t.Fatalf("wrong default page: %+v", first)
	}
	for i, got := range first.Entries {
		if got.ReportID != entries[7-i].ID {
			t.Fatal("not newest-first append order")
		}
	}
	// A new append must not shift an older page. The cursor is located before
	// filtering, even when that cursor itself does not match the child/task.
	historyAdd(t, c, children[0], "B", `{"status":"completed","summary":"new"}`)
	second := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, BeforeReportID: &first.NextBeforeReportID})
	if second.HasMore || second.NextBeforeReportID != "" || len(second.Entries) != 3 || second.Entries[0].ReportID != entries[2].ID {
		t.Fatalf("page shifted under append: %+v", second)
	}
	filtered := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, ChildID: children[1], TaskID: historyPtr("A"), Association: historyPtr("inferred"), BeforeReportID: &entries[7].ID})
	if len(filtered.Entries) != 1 || filtered.Entries[0].ReportID != entries[1].ID || filtered.Entries[0].SuppliedTaskID != "" || filtered.Entries[0].EffectiveTaskID != "A" {
		t.Fatalf("intersecting filters failed: %+v", filtered)
	}
	unassociated := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, Association: historyPtr("unassociated")})
	if len(unassociated.Entries) != 1 || unassociated.Entries[0].ReportID != entries[2].ID {
		t.Fatalf("wrong unassociated view: %+v", unassociated)
	}
	for _, task := range []string{"A", "B"} {
		out := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, ReportID: &entries[0].ID, TaskID: &task})
		want := 0
		if task == "A" {
			want = 1
		}
		if len(out.Entries) != want {
			t.Fatalf("exact ID did not intersect filter: %+v", out)
		}
	}
	var cursor *string
	seen := make(map[string]bool)
	for {
		page := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, TaskID: historyPtr("B"), Association: historyPtr("explicit"), Limit: historyPtr(2), BeforeReportID: cursor})
		for _, entry := range page.Entries {
			if seen[entry.ReportID] || entry.EffectiveTaskID != "B" || entry.Association != "explicit" {
				t.Fatalf("filtered pagination repeated or leaked an entry: %+v", page)
			}
			seen[entry.ReportID] = true
		}
		if !page.HasMore {
			break
		}
		if page.NextBeforeReportID == "" {
			t.Fatal("filtered page lacks continuation cursor")
		}
		cursor = &page.NextBeforeReportID
	}
	if len(seen) != 6 {
		t.Fatalf("filtered pages lost entries: %d", len(seen))
	}
	// Read through a newly constructed server/collector, not the live cache.
	reloaded, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.RegisterSession(parent, nil, s.logger); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries[:3] {
		out := historyRead(t, reloaded, ChildrenTasksHistoryInput{SelfID: parent, ReportID: &entry.ID})
		if len(out.Entries) != 1 {
			t.Fatal("exact ID lost after reload")
		}
		got := out.Entries[0]
		if got.ReportID != entry.ID || got.ReceivedAt != entry.ReceivedAt.Format(time.RFC3339Nano) || got.ChildID != entry.ChildID || got.SuppliedTaskID != entry.SuppliedTaskID || got.EffectiveTaskID != entry.EffectiveTaskID || got.Association != string(entry.Association) {
			t.Fatalf("identity/provenance changed: %+v", got)
		}
		// Persistence may indent JSON. Compare canonical bytes, not float64 maps.
		var a, b any
		decoder := json.NewDecoder(strings.NewReader(got.RawReport))
		decoder.UseNumber()
		if err := decoder.Decode(&a); err != nil {
			t.Fatal(err)
		}
		decoder = json.NewDecoder(strings.NewReader(string(entry.Report)))
		decoder.UseNumber()
		if err := decoder.Decode(&b); err != nil {
			t.Fatal(err)
		}
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		if string(aj) != string(bj) {
			t.Fatal("raw payload changed on reload")
		}
	}
	lossless := historyRead(t, reloaded, ChildrenTasksHistoryInput{SelfID: parent, ReportID: &entries[0].ID})
	if len(lossless.Entries[0].RawReport) <= 2048 || !strings.Contains(lossless.Entries[0].RawReport, "9007199254740993") {
		t.Fatal("history truncated or rounded unknown numeric evidence")
	}
	// Child deletion must not change historical ownership.
	if err := store.Delete(children[0]); err != nil {
		t.Fatal(err)
	}
	if got := historyRead(t, reloaded, ChildrenTasksHistoryInput{SelfID: parent, ChildID: children[0], ReportID: &entries[0].ID}); len(got.Entries) != 1 {
		t.Fatal("deleted child hid retained evidence")
	}
}

func TestChildrenTasksHistory_ValidationAndReadOnly(t *testing.T) {
	s, _, parent, children := setupParentChildSessions(t, 1)
	c, _ := s.getOrCreateCollector(parent)
	entry := historyAdd(t, c, children[0], "A", `{"status":"in_progress","summary":"evidence"}`)
	owner, _ := c.reserveWait()
	ch, err := c.startWait(owner, "A", children, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.clearWait(owner)
	before := c.historyLocked()
	firstWait := c.firstWaitTime[children[0]]
	c.writeHistory = func(childReportHistoryState) error {
		t.Error("read attempted persistence")
		return fmt.Errorf("read must not write")
	}
	for _, input := range []ChildrenTasksHistoryInput{
		{SelfID: parent},
		{SelfID: parent, Limit: historyPtr(20)},
		{SelfID: parent, BeforeReportID: &entry.ID},
	} {
		historyRead(t, s, input)
	}
	for _, input := range []ChildrenTasksHistoryInput{
		{}, {SelfID: "missing"},
		{SelfID: parent, Limit: historyPtr(0)}, {SelfID: parent, Limit: historyPtr(-1)}, {SelfID: parent, Limit: historyPtr(21)},
		{SelfID: parent, TaskID: historyPtr("")}, {SelfID: parent, Association: historyPtr("")}, {SelfID: parent, Association: historyPtr("Explicit")},
		{SelfID: parent, ReportID: &entry.ID, BeforeReportID: &entry.ID},
		{SelfID: parent, ReportID: historyPtr("missing")}, {SelfID: parent, BeforeReportID: historyPtr("missing")},
		{SelfID: parent, ReportID: historyPtr("")}, {SelfID: parent, BeforeReportID: historyPtr("")},
	} {
		_, out, err := s.handleChildrenTasksHistory(t.Context(), nil, input)
		if err != nil || out.Success || out.Error == "" || out.Entries == nil {
			t.Fatalf("invalid input accepted: %+v -> %+v %v", input, out, err)
		}
	}
	if !reflect.DeepEqual(before, c.historyLocked()) || c.waitOwner != owner || c.waitCh != ch || !owner.active || c.firstWaitTime[children[0]] != firstWait {
		t.Fatal("history read mutated wait/evidence state")
	}
	assertWaitChannel(t, ch, false)
	msgs, err := s.store.Queue(children[0]).List()
	if err != nil || len(msgs) != 0 {
		t.Fatal("history read dispatched a prompt")
	}
}

func historyProtocolServer(t *testing.T, s *Server) string {
	t.Helper()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcpServer }, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(s.mcpRequestLoggingMiddleware(h))
	t.Cleanup(ts.Close)
	return ts.URL
}

func historyMCPCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, meta mcp.Meta, out any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: meta})
	if err != nil || result.IsError {
		t.Fatalf("%s: err=%v result=%+v", name, err, result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestChildrenTasksHistory_BoundAndSharedOwnership(t *testing.T) {
	s, store, parent, children := setupParentChildSessions(t, 1)
	other := children[0] // A child is NOT authorized to read its parent's sidecar.
	c, _ := s.getOrCreateCollector(parent)
	entry := historyAdd(t, c, other, "A", `{"status":"completed","summary":"private","details":"`+strings.Repeat("x", 3000)+`","unknown":9007199254740993}`)
	endpoint := historyProtocolServer(t, s)
	p := connectBoundProtocolSession(t, endpoint, sessionBindingToken(t, s, parent))
	child := connectBoundProtocolSession(t, endpoint, sessionBindingToken(t, s, other))
	// A read needs no prompt permission, whereas even a wait-only wait still does.
	meta, err := store.GetMetadata(parent)
	if err != nil {
		t.Fatal(err)
	}
	meta.AdvancedSettings[session.FlagCanSendPrompt] = false
	if err := store.UpdateMetadata(parent, func(m *session.Metadata) { m.AdvancedSettings = meta.AdvancedSettings }); err != nil {
		t.Fatal(err)
	}
	var out ChildrenTasksHistoryOutput
	historyMCPCall(t, p, "mitto_children_tasks_history", map[string]any{"self_id": "init", "report_id": entry.ID}, nil, &out)
	if !out.Success || len(out.Entries) != 1 || out.Entries[0].ReportID != entry.ID {
		t.Fatalf("bound owner could not read without prompt flag: %+v", out)
	}
	if out.Entries[0].RawReport != string(entry.Report) || len(out.Entries[0].RawReport) <= 2048 || !strings.Contains(out.Entries[0].RawReport, "9007199254740993") {
		t.Fatal("real MCP response truncated or reinterpreted raw JSON")
	}
	var wait ChildrenTasksWaitOutput
	historyMCPCall(t, p, "mitto_children_tasks_wait", map[string]any{"self_id": "init", "children_list": children, "task_id": "A"}, nil, &wait)
	if wait.Success || !strings.Contains(wait.Error, "Can Send Prompt") {
		t.Fatalf("read relaxed wait permission: %+v", wait)
	}
	for _, args := range []map[string]any{
		{"self_id": parent, "report_id": entry.ID},
		{"self_id": "init", "child_id": other},
		{"self_id": "init", "before_report_id": entry.ID},
	} {
		historyMCPCall(t, child, "mitto_children_tasks_history", args, nil, &out)
		if len(out.Entries) != 0 {
			t.Fatalf("child spoof leaked parent history: %+v", out)
		}
	}
	// Each caller may itself own a collector. Neither an exact ID nor a child
	// filter can redirect the parent's read into that other collector.
	otherCollector, _ := s.getOrCreateCollector(other)
	foreign := historyAdd(t, otherCollector, "retained-deleted-grandchild", "A", `{"status":"completed","summary":"other namespace"}`)
	var foreignError string
	for _, id := range []string{foreign.ID, "nonexistent"} {
		historyMCPCall(t, p, "mitto_children_tasks_history", map[string]any{"self_id": "init", "report_id": id}, nil, &out)
		if out.Success || len(out.Entries) != 0 || out.Error == "" || (foreignError != "" && foreignError != out.Error) {
			t.Fatalf("foreign ID leaked existence or evidence: %+v", out)
		}
		foreignError = out.Error
	}
	historyMCPCall(t, p, "mitto_children_tasks_history", map[string]any{"self_id": "init", "child_id": foreign.ChildID}, nil, &out)
	if !out.Success || len(out.Entries) != 0 {
		t.Fatalf("child filter crossed owner namespace: %+v", out)
	}
	// One shared protocol session routes per-call attested identity, never self_id.
	if !s.BindACPSession(parent, "ses_history_parent") || !s.BindACPSession(other, "ses_history_child") {
		t.Fatal("ACP binding failed")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "history-shared", Version: "1"}, nil)
	shared, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: secretRoundTripper{secret: s.SharedMCPSecret()}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Close() })
	for _, tc := range []struct {
		self, acp string
		want      bool
	}{
		{"init", "ses_history_parent", true},
		{"init", "ses_history_child", false},
		{parent, "ses_history_child", false},
		{parent, "", false},
		{parent, "ses_unknown", false},
		{other, "ses_history_parent", false},
	} {
		historyMCPCall(t, shared, "mitto_children_tasks_history", map[string]any{"self_id": tc.self, "report_id": entry.ID}, mcp.Meta{OpenCodeCallerMetaKey: tc.acp}, &out)
		if out.Success != tc.want || (tc.want && len(out.Entries) != 1) || (!tc.want && len(out.Entries) != 0) {
			t.Fatalf("shared identity (%q, %q): %+v", tc.self, tc.acp, out)
		}
	}
	s.UnregisterSession(other)
	_, out, err = s.handleChildrenTasksHistory(t.Context(), sharedReq(s, "ses_history_child"), ChildrenTasksHistoryInput{SelfID: other})
	if err != nil || out.Success || len(out.Entries) != 0 {
		t.Fatal("unregistered caller retained history authority")
	}
}

func TestChildrenTasksHistory_ConcurrentAppends(t *testing.T) {
	s, _, parent, children := setupParentChildSessions(t, 1)
	c, _ := s.getOrCreateCollector(parent)
	old := historyAdd(t, c, children[0], "A", `{"status":"failed","summary":"old"}`)
	boundary := historyAdd(t, c, children[0], "A", `{"status":"completed","summary":"boundary"}`)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			historyAdd(t, c, children[0], "A", `{"status":"in_progress","summary":"concurrent"}`)
		}
	}()
	close(start)
	for i := 0; i < 20; i++ {
		out := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, BeforeReportID: &boundary.ID})
		if len(out.Entries) != 1 || out.Entries[0].ReportID != old.ID || out.HasMore {
			t.Fatalf("append shifted stable cursor: %+v", out)
		}
	}
	wg.Wait()
}

func TestChildrenTasksHistory_LargeHistoryLeavesWaitCompact(t *testing.T) {
	s, _, parent, children := setupParentChildSessions(t, 1)
	child := children[0]
	for i := 0; i < 21; i++ {
		_, out, err := s.handleChildrenTasksReport(t.Context(), nil, ChildrenTasksReportInput{
			SelfID:  child,
			Status:  "in_progress",
			Summary: fmt.Sprintf("retained-history-%02d-%s", i, strings.Repeat("x", 256)),
		})
		if err != nil || !out.Success {
			t.Fatalf("retain report %d: %+v %v", i, out, err)
		}
	}

	// The history endpoint returns the bounded retained evidence; a wait must
	// expose only its selected task result, never this accumulated history.
	if got := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent}); len(got.Entries) != 5 || !got.HasMore {
		t.Fatalf("default history page: %+v", got)
	}
	if got := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, Limit: historyPtr(1)}); len(got.Entries) != 1 || !got.HasMore {
		t.Fatalf("one-entry history page: %+v", got)
	}
	if got := historyRead(t, s, ChildrenTasksHistoryInput{SelfID: parent, Limit: historyPtr(20)}); len(got.Entries) != 20 || !got.HasMore {
		t.Fatalf("twenty-entry history page: %+v", got)
	}

	started, release := make(chan struct{}), make(chan struct{})
	s.sessionManager = &reportWaitBarrierManager{
		mockSessionManagerForAutoResume: &mockSessionManagerForAutoResume{},
		onWaiting: func(waiting bool) {
			if waiting {
				close(started)
				<-release
			}
		},
	}
	result := startTaskScopeWait(t, s, t.Context(), ChildrenTasksWaitInput{
		SelfID: parent, ChildrenList: children, TaskID: "B", Prompt: "finish B", TimeoutSeconds: 5,
	})
	<-started
	_, reported, err := s.handleChildrenTasksReport(t.Context(), nil, ChildrenTasksReportInput{
		SelfID: child, TaskID: "B", Status: "completed", Summary: "selected B result",
	})
	if err != nil || !reported.Success {
		t.Fatalf("selected report: %+v %v", reported, err)
	}
	close(release)
	got := <-result
	if got.err != nil || !got.output.Success || got.output.TimedOut || len(got.output.Reports) != 1 ||
		got.output.Reports[child].Report == nil || got.output.Reports[child].Report.Summary != "selected B result" ||
		!got.output.Reports[child].Completed {
		t.Fatalf("wait did not select B compactly: %+v %v", got.output, got.err)
	}
	encoded, err := json.Marshal(got.output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "retained-history-") || strings.Contains(string(encoded), "raw_report") {
		t.Fatalf("wait expanded retained history: %s", encoded)
	}
}
