package acpbackend

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// newTestSession creates a Connection with one registered session backed by
// fp, returning the SessionRef the tests drive SessionOps calls through.
func newTestSession(t *testing.T, fp *fakeSharedProcess) (*Connection, agentbackend.SessionRef) {
	t.Helper()
	fp.newSessionHandle = &conversation.SessionHandle{SessionID: "upstream-1"}
	c := NewConnection(fp, "acp", "/work", nil)
	sess, err := c.NewSession(context.Background(), "acp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return c, sess.Ref()
}

func TestSessionOps_Prompt(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.promptResp = agentbackend.PromptOutcome{StopReason: agentbackend.StopReasonEndTurn}
	c, ref := newTestSession(t, fp)

	content := []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: "hello"}}}
	outcome, err := c.Prompt(context.Background(), ref, content)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if outcome.StopReason != agentbackend.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", outcome.StopReason)
	}
	if len(fp.promptArgs) != 1 || fp.promptArgs[0].Text == nil || fp.promptArgs[0].Text.Text != "hello" {
		t.Errorf("underlying Prompt did not receive the translated content: %+v", fp.promptArgs)
	}
}

func TestSessionOps_Prompt_UnknownRef(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	_, err := c.Prompt(context.Background(), agentbackend.SessionRef{ConversationID: "ghost"}, nil)
	if !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for an unregistered ref, got %v", err)
	}
}

func TestSessionOps_Cancel(t *testing.T) {
	fp := newFakeSharedProcess()
	c, ref := newTestSession(t, fp)
	if err := c.Cancel(context.Background(), ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(fp.cancelCalls) != 1 || fp.cancelCalls[0] != "upstream-1" {
		t.Errorf("underlying Cancel not called with the mapped session id: %+v", fp.cancelCalls)
	}
}

func TestSessionOps_SetModel_AckGated(t *testing.T) {
	fp := newFakeSharedProcess()
	c, ref := newTestSession(t, fp)
	if err := c.SetModel(context.Background(), ref, "model-b"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if len(fp.setModelCalls) != 1 || fp.setModelCalls[0].model != "model-b" {
		t.Errorf("underlying SetSessionModel not called as expected: %+v", fp.setModelCalls)
	}
}

func TestSessionOps_SetModel_UnsupportedTranslatesFeature(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.setModelErr = acp.NewMethodNotFound("session/set_model")
	c, ref := newTestSession(t, fp)

	err := c.SetModel(context.Background(), ref, "model-b")
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedError, got %v", err)
	}
	if unsupported.Feature != agentbackend.FeatureModelSelection {
		t.Errorf("Feature = %q, want model_selection", unsupported.Feature)
	}
}

func TestSessionOps_SetMode_AckGated(t *testing.T) {
	fp := newFakeSharedProcess()
	c, ref := newTestSession(t, fp)
	if err := c.SetMode(context.Background(), ref, "code"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if len(fp.setModeCalls) != 1 || fp.setModeCalls[0].mode != "code" {
		t.Errorf("underlying SetSessionMode not called as expected: %+v", fp.setModeCalls)
	}
}

func TestSessionOps_SetMode_UnsupportedTranslatesFeature(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.setModeErr = acp.NewMethodNotFound("session/set_mode")
	c, ref := newTestSession(t, fp)

	err := c.SetMode(context.Background(), ref, "code")
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedError, got %v", err)
	}
	if unsupported.Feature != agentbackend.FeatureModeSelection {
		t.Errorf("Feature = %q, want mode_selection", unsupported.Feature)
	}
}

func TestSessionOps_Cancel_UnknownRef(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	if err := c.Cancel(context.Background(), agentbackend.SessionRef{ConversationID: "ghost"}); !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

// configSettingFakeProcess adds the optional conversation.ConfigOptionSetter
// capability to the shared fake.
type configSettingFakeProcess struct {
	*fakeSharedProcess
	gotSession, gotID, gotValue string
	result                      []conversation.SessionConfigOption
	err                         error
}

func (p *configSettingFakeProcess) SetSessionConfigOption(_ context.Context, sessionID, configID, value string) ([]conversation.SessionConfigOption, error) {
	p.gotSession, p.gotID, p.gotValue = sessionID, configID, value
	return p.result, p.err
}

func newConfigSetterSession(t *testing.T, p *configSettingFakeProcess) (*Connection, agentbackend.SessionRef) {
	t.Helper()
	p.fakeSharedProcess.newSessionHandle = &conversation.SessionHandle{SessionID: "upstream-1"}
	c := NewConnection(p, "acp", "/work", nil)
	sess, err := c.NewSession(context.Background(), "acp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return c, sess.Ref()
}

func TestSessionOps_SetConfigOption_ReturnsFullSnapshot(t *testing.T) {
	p := &configSettingFakeProcess{
		fakeSharedProcess: newFakeSharedProcess(),
		result: []conversation.SessionConfigOption{
			{ID: "effort", Category: "thought_level", Type: "select", CurrentValue: "high",
				Options: []conversation.SessionConfigOptionValue{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}},
			{ID: "model", Category: "model", Type: "select", CurrentValue: "m-1"},
		},
	}
	c, ref := newConfigSetterSession(t, p)

	opts, err := c.SetConfigOption(context.Background(), ref, "effort", "high")
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	if p.gotSession != "upstream-1" || p.gotID != "effort" || p.gotValue != "high" {
		t.Errorf("underlying call = (%q,%q,%q)", p.gotSession, p.gotID, p.gotValue)
	}
	if len(opts) != 2 || opts[0].ID != "effort" || opts[0].Current != "high" || len(opts[0].Values) != 2 || opts[1].ID != "model" {
		t.Errorf("neutral snapshot not preserved in agent order: %+v", opts)
	}
}

func TestSessionOps_SetConfigOption_Unsupported(t *testing.T) {
	c, ref := newTestSession(t, newFakeSharedProcess())
	_, err := c.SetConfigOption(context.Background(), ref, "effort", "high")
	var ue *agentbackend.UnsupportedError
	if !errors.As(err, &ue) || ue.Feature != agentbackend.FeatureConfigSelection {
		t.Fatalf("expected UnsupportedError(config_selection), got %v", err)
	}
}

func TestSessionOps_SetConfigOption_SetterErrorAndUnknownRef(t *testing.T) {
	p := &configSettingFakeProcess{fakeSharedProcess: newFakeSharedProcess(), err: errors.New("rejected")}
	c, ref := newConfigSetterSession(t, p)
	if _, err := c.SetConfigOption(context.Background(), ref, "effort", "bogus"); err == nil {
		t.Fatal("expected setter error to propagate")
	}
	_, err := c.SetConfigOption(context.Background(), agentbackend.SessionRef{ConversationID: "ghost"}, "effort", "x")
	if !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}
