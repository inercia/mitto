package chatui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/inercia/mitto/pkg/api"
)

// programSender is the subset of *tea.Program used by the pump, so tests
// can substitute a fake without spinning up a real terminal program.
type programSender interface {
	Send(tea.Msg)
}

// pump drains evCh into program.Send(eventMsg{...}) until it closes, then
// sends exactly one streamEndMsg carrying the terminal error from errCh —
// mirroring crush's "goroutine calls program.Send per decoded event"
// pattern (internal/cmd/root.go:134 in that repo) and the exact
// termination contract Session.EventsChan documents (pkg/api/stream.go):
// errc receives exactly one terminal error before out closes.
//
// Reconnect/backoff are explicitly NOT implemented here (Plan Scope: "no
// reconnect until mitto-rwxq.5 lands") — streamEndMsg always ends the
// program's connection to the server; Update degrades the status line and
// quits.
//
// RunPump is exported so the CLI bootstrap (internal/cmd/conversation_chat.go)
// can start it as `go chatui.RunPump(...)` right after tea.NewProgram, per
// the crush pattern: the pump lives outside the program.
func RunPump(ctx context.Context, evCh <-chan api.Event, errCh <-chan error, program programSender) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-evCh:
			if !ok {
				program.Send(streamEndMsg{err: <-errCh})
				return
			}
			program.Send(eventMsg{event: ev})
		}
	}
}
