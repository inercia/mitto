// Package chatui implements the full-screen Bubble Tea chat TUI for
// `mitto conversation chat` (mitto-pscc.7). It is CLI-owned, mirroring
// internal/termmd's ownership rule: internal/conversation must never import
// this package.
//
// Structure mirrors charmbracelet/crush (verified against that repo,
// 2026-08-09, recorded on the bead's Plan comment): one tea.Model (Model in
// model.go), sub-components as plain imperative structs (transcript.go,
// statusline.go, permission.go) driven directly by the root Update, and an
// event-pump goroutine (pump.go) outside the program that feeds
// Session.EventsChan into program.Send. Update never performs I/O; every
// server call is issued as a tea.Cmd.
package chatui

import (
	"github.com/inercia/mitto/pkg/api"
)

// eventMsg wraps a single streamed api.Event from the event pump.
type eventMsg struct {
	event api.Event
}

// streamEndMsg reports that the event stream (Session.EventsChan) has
// terminated — connection lost, context cancelled, or the session was
// deleted server-side. err is always non-nil (the terminal error the SDK
// guarantees on stream end). Until mitto-rwxq.5 lands there is no
// reconnect: receiving this message ends the program.
type streamEndMsg struct {
	err error
}

// sendDoneMsg reports the outcome of a Session.SendPrompt call issued as a
// tea.Cmd from the input textarea's submit handling. err is nil on success.
type sendDoneMsg struct {
	err error
}

// cancelDoneMsg reports the outcome of a Session.Cancel call (esc key).
type cancelDoneMsg struct {
	err error
}

// permAnsweredMsg reports the outcome of a Session.AnswerPermission call
// issued from the permission modal.
type permAnsweredMsg struct {
	requestID string
	err       error
}
