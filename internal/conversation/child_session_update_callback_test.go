package conversation

import (
	"context"
	"log/slog"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

func TestChildSessionUpdateCallbackRequiresRegistryAdmission(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	defer store.Close()

	srv, err := mcpserver.NewServer(mcpserver.Config{Port: 0}, mcpserver.Dependencies{Store: store})
	if err != nil {
		t.Fatalf("mcpserver.NewServer: %v", err)
	}

	newOwner := func(rootID acp.SessionId) (func(context.Context, ChildSessionUpdate) error, *[]string) {
		owner := session.GenerateSessionID()
		if err := store.Create(session.Metadata{SessionID: owner, Name: owner, ACPServer: "opencode", WorkingDir: t.TempDir()}); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
		if err := srv.RegisterSession(owner, nil, slog.Default()); err != nil {
			t.Fatalf("RegisterSession: %v", err)
		}
		generation, ok := srv.BindACPRootGeneration(owner, string(rootID))
		if !ok {
			t.Fatal("BindACPRootGeneration failed")
		}
		var ownerDelivered []string
		client := NewWebClient(WebClientConfig{
			OnAvailableCommands: func(commands []AvailableCommand) {
				for _, command := range commands {
					ownerDelivered = append(ownerDelivered, command.Name)
				}
			},
		})
		t.Cleanup(client.Close)
		bs := &BackgroundSession{persistedID: owner, globalMcpServer: srv}
		return bs.childSessionUpdateCallback(client, string(rootID), generation), &ownerDelivered
	}
	rootA, rootB := acp.SessionId("open-code-root-a"), acp.SessionId("open-code-root-b")
	callbackA, deliveredA := newOwner(rootA)
	callbackB, deliveredB := newOwner(rootB)
	if callbackA == nil || callbackB == nil {
		t.Fatal("child update callback = nil")
	}

	projected := acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
		AvailableCommands: []acp.AvailableCommand{{Name: "child-command"}},
	}}
	update := ChildSessionUpdate{
		RootSessionID: rootA, ChildSessionID: "child-session-a", ParentSessionID: rootA, Depth: 1,
		Type: "update", Update: &projected,
	}
	if err := callbackA(context.Background(), update); err != nil {
		t.Fatalf("unadmitted update: %v", err)
	}
	if len(*deliveredA) != 0 {
		t.Fatalf("unadmitted child update reached UI: %v", *deliveredA)
	}

	created := update
	created.Type, created.Status, created.Update = "status", "created", nil
	if err := callbackA(context.Background(), created); err != nil {
		t.Fatalf("created status: %v", err)
	}
	if err := callbackA(context.Background(), update); err != nil {
		t.Fatalf("admitted update: %v", err)
	}
	if len(*deliveredA) != 1 || (*deliveredA)[0] != "child-command" {
		t.Fatalf("delivered child update = %v, want [child-command] exactly once", *deliveredA)
	}
	if len(*deliveredB) != 0 {
		t.Fatalf("conversation B received conversation A child update: %v", *deliveredB)
	}

	// Children are attributed through their parent chain, not by selecting a
	// callback based on untrusted child input.
	nested := ChildSessionUpdate{RootSessionID: rootA, ChildSessionID: "grandchild-a", ParentSessionID: "child-session-a", Depth: 2, Type: "status", Status: "created"}
	if err := callbackA(context.Background(), nested); err != nil {
		t.Fatalf("nested child status: %v", err)
	}
	nestedUpdate := update
	nestedUpdate.ChildSessionID, nestedUpdate.ParentSessionID, nestedUpdate.Depth = "grandchild-a", "child-session-a", 2
	if err := callbackA(context.Background(), nestedUpdate); err != nil {
		t.Fatalf("nested child update: %v", err)
	}
	if len(*deliveredA) != 2 || (*deliveredA)[1] != "child-command" {
		t.Fatalf("nested child update was not delivered once to A: %v", *deliveredA)
	}

	foreign := update
	foreign.RootSessionID = rootB
	if err := callbackA(context.Background(), foreign); err != nil {
		t.Fatalf("foreign root update: %v", err)
	}
	if len(*deliveredA) != 2 || len(*deliveredB) != 0 {
		t.Fatalf("foreign root update escaped owner isolation: A=%v B=%v", *deliveredA, *deliveredB)
	}

	terminal := update
	terminal.Type, terminal.Status, terminal.Update = "status", "completed", nil
	if err := callbackA(context.Background(), terminal); err != nil {
		t.Fatalf("terminal status: %v", err)
	}
	if err := callbackA(context.Background(), update); err != nil {
		t.Fatalf("late update: %v", err)
	}
	if len(*deliveredA) != 2 {
		t.Fatalf("terminal child update reached UI after revocation: %v", *deliveredA)
	}
}
