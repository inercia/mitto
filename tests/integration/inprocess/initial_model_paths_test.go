//go:build integration

package inprocess

import (
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// Real SessionManager/ACP handshakes: the MCP creation entry point must choose
// exactly the same initial model as REST and ordinary queued-session startup.
func TestInitialModelSelectionAcrossEntryPoints(t *testing.T) {
	const want = "claude-opus-4-6"
	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces[0].InitialModelTag = "Cheap"
		c.Workspaces[0].ModelOverrides = config.ModelOverrides{
			{ACPServer: c.Workspaces[0].ACPServer, Tag: "Cheap", ModelID: want},
		}
	})
	sm := ts.Server.GetSessionManager()
	ws := sm.GetWorkspaces()[0]
	for _, path := range []string{"REST", "MCP plain text", "MCP named prompt", "queue retry", "auto child"} {
		t.Run(path, func(t *testing.T) {
			var bs *conversation.BackgroundSession
			var id string
			var err error
			if path == "REST" {
				created, createErr := ts.Client.CreateSession(api.CreateSessionRequest{Name: path})
				if createErr != nil {
					t.Fatal(createErr)
				}
				id = created.SessionID
				bs = sm.GetSession(id)
			} else {
				id = session.GenerateSessionID()
				if err := ts.Store.Create(session.Metadata{SessionID: id, Name: path, ACPServer: ws.ACPServer, WorkingDir: ws.WorkingDir}); err != nil {
					t.Fatal(err)
				}
				switch path {
				case "MCP plain text":
					bs, err = sm.ResumeSessionWithInitialPrompt(id, path, ws.WorkingDir, "")
				case "MCP named prompt":
					sm.SetPreferredModelsResolver(func(string, string) []config.PromptPreferredModel {
						return []config.PromptPreferredModel{{ModelTag: "unmatched"}}
					})
					bs, err = sm.ResumeSessionWithInitialPrompt(id, path, ws.WorkingDir, "review")
				case "auto child":
					bs, err = sm.ResumeSessionWithInitialModelPreference(id, path, ws.WorkingDir, nil)
				default:
					bs, err = sm.ResumeSession(id, path, ws.WorkingDir)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if bs == nil {
				t.Fatal("session not running")
			}
			done := make(chan error, 1)
			if err := bs.PromptWithMeta("hello", conversation.PromptMeta{OnComplete: func(err error) { done <- err }}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("prompt timed out")
			}
			meta, err := ts.Store.GetMetadata(id)
			if err != nil {
				t.Fatal(err)
			}
			if bs.GetBaselineModel() != want || bs.AgentModels().CurrentModelId != want || meta.BaselineModel != want {
				t.Fatalf("baseline=%q agent=%q persisted=%q; want %q", bs.GetBaselineModel(), bs.AgentModels().CurrentModelId, meta.BaselineModel, want)
			}
			if len(meta.PendingInitialModelPreference) != 0 {
				t.Fatal("startup preference not consumed")
			}
		})
	}
}
