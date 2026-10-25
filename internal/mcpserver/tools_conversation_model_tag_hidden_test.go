package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/mcpserver"
)

func TestConversationUpdate_ModelTag_HiddenModelRefusedWithoutSideEffects(t *testing.T) {
	for _, prompting := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "prompting"}[prompting], func(t *testing.T) {
			harness := mcpserver.NewModelTagCallerTestHarness(t)
			var events []string
			bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
				IsPrompting: prompting,
				ACPServer:   "test-server",
				MittoConfig: &config.Config{
					ACPServers: []config.ACPServer{{Name: "test-server", Command: "x", HiddenModels: []string{"^hidden$"}}},
					Models: []config.ModelProfile{{
						Name:     "Hidden coding",
						Tags:     []string{"Coding"},
						Criteria: &config.ACPServerConstraint{Pattern: "hidden", MatchMode: "exact"},
					}},
				},
				AgentModels: &conversation.SessionModelState{
					CurrentModelId:  "hidden",
					AvailableModels: []conversation.ModelInfo{{ModelId: "hidden", Name: "Hidden model"}},
				},
				ConfigOptions: []conversation.SessionConfigOption{{
					ID: string(conversation.ModelConfigId), Category: conversation.ConfigOptionCategoryModel,
					CurrentValue: "hidden", Options: []conversation.SessionConfigOptionValue{{Value: "hidden", Name: "Hidden model"}},
				}},
				OnModelSelectionEvent: func(kind, value string) { events = append(events, kind+":"+value) },
			})
			targetID := harness.AddRunningTarget(t, bs)
			out, err := harness.UpdateModelTag(context.Background(), targetID, "Coding")
			if err != nil || out.Success || !strings.Contains(out.Error, "did not resolve") {
				t.Fatalf("result=%+v err=%v", out, err)
			}
			if len(events) != 0 || bs.GetBaselineModel() != "" {
				t.Fatalf("events=%v baseline=%q", events, bs.GetBaselineModel())
			}
		})
	}
}
