package conversation

import (
	"errors"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type completionNoticeDeps struct {
	*fakePromptDeps
	recordErr error
	order     []string
	count     int
}

func (d *completionNoticeDeps) pdRecordErrorEvent(seq int64, msg string) error {
	if d.recordErr != nil {
		return d.recordErr
	}
	d.eventCount++
	return d.fakePromptDeps.pdRecordErrorEvent(seq, msg)
}

func (d *completionNoticeDeps) pdNotifyObservers(fn func(SessionObserver)) {
	fn(&completionNoticeObserver{pdRecorderObserver: &pdRecorderObserver{deps: d.fakePromptDeps}, deps: d})
}

type completionNoticeObserver struct {
	*pdRecorderObserver
	deps *completionNoticeDeps
}

func (o *completionNoticeObserver) OnError(msg string) {
	o.deps.order = append(o.deps.order, "error")
	o.pdRecorderObserver.OnError(msg)
}

func (o *completionNoticeObserver) OnPromptComplete(count int) {
	o.deps.order = append(o.deps.order, "complete")
	o.deps.count = count
}

func TestPromptDispatcher_CompletionNoticeBeforeComplete(t *testing.T) {
	for _, recordErr := range []error{nil, errors.New("disk unavailable")} {
		d := &completionNoticeDeps{fakePromptDeps: newFakePromptDeps(), recordErr: recordErr}
		d.hasRecorder = true
		d.eventCount = 3
		(promptDispatcher{}).handlePromptSuccess(d, 3, 1,
			acp.PromptResponse{StopReason: acp.StopReasonRefusal}, "status?", PromptMeta{}, time.Now(), time.Now())
		if strings.Join(d.order, ",") != "error,complete" || len(d.notifiedErrors) != 1 {
			t.Fatalf("must deliver error before completing, even if persistence fails: %v %v", d.order, d.notifiedErrors)
		}
		wantCount := 4
		if recordErr != nil {
			wantCount = 3
		}
		if d.count != wantCount {
			t.Fatalf("completion event count = %d, want %d", d.count, wantCount)
		}
	}
}

func TestPromptDispatcher_UnsuccessfulCompletionSurfacesError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason acp.StopReason
		want   string
	}{
		{"refusal", acp.StopReasonRefusal, "refused"},
		{"token limit", acp.StopReasonMaxTokens, "token limit"},
		{"request limit", acp.StopReasonMaxTurnRequests, "request limit"},
		{"unknown", acp.StopReason("future_failure"), "did not provide a reason"},
		{"missing reason", acp.StopReason(""), "did not provide a reason"},
	} {
		for _, partial := range []string{"", "Partial assistant response"} {
			t.Run(tc.name+"/"+partial, func(t *testing.T) {
				d := newFakePromptDeps()
				d.hasRecorder = true
				d.lastAgentMessage = partial
				idle := (promptDispatcher{}).handlePromptSuccess(d, 0, 1,
					acp.PromptResponse{StopReason: tc.reason}, "status?", PromptMeta{}, time.Now(), time.Now())
				if len(d.notifiedErrors) != 1 || !strings.Contains(d.notifiedErrors[0], tc.want) {
					t.Fatalf("expected visible %q explanation, got %v", tc.want, d.notifiedErrors)
				}
				if len(d.recordedErrorEvents) != 1 || d.recordedErrorEvents[0] != d.notifiedErrors[0] {
					t.Fatalf("expected matching durable error, got %v", d.recordedErrorEvents)
				}
				if d.refreshSeqCalls != 1 {
					t.Fatalf("expected sequence refresh after recording, got %d", d.refreshSeqCalls)
				}
				if idle || len(d.followUpCalls) != 0 {
					t.Fatal("unsuccessful completion must not trigger successful-turn automation")
				}
			})
		}
	}
}

func TestPromptDispatcher_CompletionErrorWithoutRecorder(t *testing.T) {
	d := newFakePromptDeps()
	(promptDispatcher{}).handlePromptSuccess(d, 0, 1,
		acp.PromptResponse{StopReason: acp.StopReasonRefusal}, "status?", PromptMeta{}, time.Now(), time.Now())
	if len(d.notifiedErrors) != 1 || len(d.recordedErrorEvents) != 0 {
		t.Fatalf("must notify even without storage: errors=%v recorded=%v", d.notifiedErrors, d.recordedErrorEvents)
	}
}

func TestPromptDispatcher_NormalCompletionDoesNotSurfaceError(t *testing.T) {
	for _, reason := range []acp.StopReason{acp.StopReasonEndTurn, acp.StopReasonCancelled} {
		t.Run(string(reason), func(t *testing.T) {
			d := newFakePromptDeps()
			d.hasRecorder = true
			(promptDispatcher{}).handlePromptSuccess(d, 0, 1,
				acp.PromptResponse{StopReason: reason}, "status?", PromptMeta{}, time.Now(), time.Now())
			if len(d.notifiedErrors) != 0 || len(d.recordedErrorEvents) != 0 {
				t.Fatalf("normal completion/cancellation must not be an error: %v", d.notifiedErrors)
			}
		})
	}
}
