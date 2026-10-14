package conversation

import (
	"errors"
	"strings"
	"testing"
)

type processorAuthNoticeObserver struct {
	SessionObserver
	notifications []UINotifyRequest
}

func (o *processorAuthNoticeObserver) OnNotification(req UINotifyRequest) {
	o.notifications = append(o.notifications, req)
}

func TestFormatProcessorFailureNotice_Table(t *testing.T) {
	const authSecret = "AUTH-BLOB-DO-NOT-SHOW"
	fallback := UINotifyRequest{Title: "After-phase processor failed", Message: "existing wording", Style: "warning"}
	tests := []struct {
		name       string
		err        error
		wantAuth   bool
		wantSecret string
	}{
		{
			name:       "JSON-RPC auth error",
			err:        errors.New(`ACP error: {"code":-32000,"message":"Authentication required","token":"` + authSecret + `"}`),
			wantAuth:   true,
			wantSecret: authSecret,
		},
		{name: "plain auth error", err: errors.New("authentication required"), wantAuth: true},
		{name: "non-auth error", err: errors.New("network timeout")},
		{name: "nil error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, send := FormatProcessorFailureNotice(t.TempDir(), "Research workspace", tt.err, fallback)
			if !send {
				t.Fatal("notice was unexpectedly suppressed")
			}
			if !tt.wantAuth {
				if got != fallback {
					t.Fatalf("notice = %+v, want fallback %+v", got, fallback)
				}
				return
			}

			if got.Title != processorAuthNoticeTitle {
				t.Fatalf("notice title = %q, want %q", got.Title, processorAuthNoticeTitle)
			}
			for _, want := range []string{"Research workspace", "authentication has expired", "Re-authenticate the CLI", "claude auth login", "auggie auth login"} {
				if !strings.Contains(got.Message, want) {
					t.Errorf("notice message %q does not contain %q", got.Message, want)
				}
			}
			if tt.wantSecret != "" && strings.Contains(got.Message, tt.wantSecret) {
				t.Fatalf("auth notice exposed raw authentication details: %q", got.Message)
			}
			if got.Style != "warning" {
				t.Errorf("notice style = %q, want warning", got.Style)
			}
		})
	}
}

func TestBackgroundSessionProcessorFailureNotifyFunc_DeduplicatesAuthNotice(t *testing.T) {
	workspaceUUID := t.TempDir()
	bs := &BackgroundSession{workspaceUUID: workspaceUUID, workspaceName: "Live workspace", uiPromptCtr: uiPromptCenter{}}
	observer := &processorAuthNoticeObserver{}
	bs.AddObserver(observer)

	notify := bs.processorFailureNotifyFunc()
	lastErr := errors.New(`auxiliary prompt failed: {"code":-32000,"message":"Authentication required","token":"AUTH-BLOB-DO-NOT-SHOW"}`)
	notify(workspaceUUID, "identify-user-data", lastErr)
	notify(workspaceUUID, "identify-user-data", lastErr)

	if len(observer.notifications) != 1 {
		t.Fatalf("notifications = %d, want one during auth cooldown: %+v", len(observer.notifications), observer.notifications)
	}
	got := observer.notifications[0]
	if got.Title != processorAuthNoticeTitle || !strings.Contains(got.Message, "Live workspace") {
		t.Fatalf("notification = %+v, want safe notice for Live workspace", got)
	}
	if strings.Contains(got.Message, "AUTH-BLOB-DO-NOT-SHOW") {
		t.Fatalf("notification exposed raw authentication details: %q", got.Message)
	}

	otherWorkspaceUUID := t.TempDir()
	other := &BackgroundSession{workspaceUUID: otherWorkspaceUUID, workspaceName: "Other workspace", uiPromptCtr: uiPromptCenter{}}
	otherObserver := &processorAuthNoticeObserver{}
	other.AddObserver(otherObserver)
	other.processorFailureNotifyFunc()(otherWorkspaceUUID, "identify-user-data", lastErr)
	if len(otherObserver.notifications) != 1 {
		t.Fatalf("other workspace notifications = %d, want one independent notice", len(otherObserver.notifications))
	}
}
