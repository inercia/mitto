package chatui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/inercia/mitto/pkg/api"
)

// permissionModal is the overlay state for an in-flight EventPermission
// request. Permission requests are answered in a modal, not inline in the
// transcript, per the Plan's Scope. Only one request is modal at a time —
// if a second arrives while one is open it is queued and shown next.
type permissionModal struct {
	sess    *api.Session
	pending []api.Event // queued permission events, front is the one shown
	styles  *styles
	width   int
}

func newPermissionModal(sess *api.Session, styles *styles) *permissionModal {
	return &permissionModal{sess: sess, styles: styles}
}

func (p *permissionModal) SetWidth(w int) { p.width = w }

// Push enqueues a permission request. Returns true if it is now the one
// being shown (i.e. the modal transitioned from closed to open).
func (p *permissionModal) Push(ev api.Event) bool {
	wasEmpty := len(p.pending) == 0
	p.pending = append(p.pending, ev)
	return wasEmpty
}

// Open reports whether a permission request is currently modal.
func (p *permissionModal) Open() bool { return len(p.pending) > 0 }

// current returns the request currently shown, or the zero Event if none.
func (p *permissionModal) current() api.Event {
	if len(p.pending) == 0 {
		return api.Event{}
	}
	return p.pending[0]
}

// Answer answers the currently-shown request as a tea.Cmd (no I/O in
// Update itself) and advances to the next queued request, if any.
func (p *permissionModal) Answer(approved bool) tea.Cmd {
	if len(p.pending) == 0 {
		return nil
	}
	cur := p.pending[0]
	p.pending = p.pending[1:]
	return func() tea.Msg {
		return permAnsweredMsg{requestID: cur.RequestID, err: p.sess.AnswerPermission(cur.RequestID, approved)}
	}
}

func (p *permissionModal) Render() string {
	cur := p.current()
	body := p.styles.warningStyle.Render(cur.Title) + "\n\n" +
		p.styles.agentStyle.Render(cur.Description) + "\n\n" +
		p.styles.successStyle.Render("[y] approve") + "   " +
		p.styles.errorStyle.Render("[n] deny")
	return p.styles.modalBorder.Width(p.width - 4).Render(body)
}
