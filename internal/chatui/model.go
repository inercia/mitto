package chatui

import (
	"fmt"
	"strings"

	textarea "charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/inercia/mitto/internal/termmd"
	"github.com/inercia/mitto/pkg/api"
)

// Options configures a new chat Model. Constructed by the CLI command
// (internal/cmd/conversation_chat.go) from resolved flags.
type Options struct {
	// Title is the conversation title shown in the status line.
	Title string
	// NoColor selects termmd.ModePlain regardless of terminal detection —
	// forwarded from --no-color/$NO_COLOR (Plan decision: never disables
	// rendering, only styling).
	NoColor bool
	// ShowThoughts, when false (the --no-thoughts flag), drops
	// EventAgentThought items at append time.
	ShowThoughts bool
}

// Model is the sole tea.Model for the chat TUI (crush pattern: one model,
// sub-components as imperative structs called directly from Update/View —
// never nested models, never message routing between sub-components).
type Model struct {
	sess *api.Session

	transcript *transcript
	input      textarea.Model
	status     *statusLine
	perm       *permissionModal
	styles     *styles

	// history and completion restore the two readline affordances
	// bubbles/textarea provides neither of (mitto-pscc.11): up/down input
	// recall and slash-command tab completion. See handleKey for the
	// routing precedence between them, the textarea, and the permission
	// modal.
	history    *inputHistory
	completion *completionMenu

	width, height int
	inFlight      bool
	quitting      bool
	quitErr       error

	// clientIDFn overrides the session-derived client ID in tests, which run
	// without a real *api.Session.
	clientIDFn func() string
}

// NewModel builds the chat Model. sess may be nil at construction time and
// supplied later via SetSession, since the CLI bootstrap constructs the
// Model before Connect (SeedHistory is wired into the OnEventsLoaded
// callback passed to Connect) — see internal/cmd/conversation_chat.go. sess
// must be set (directly or via SetSession) before Init/Update run, i.e.
// before tea.NewProgram(model).Run() is called.
func NewModel(sess *api.Session, opts Options) *Model {
	st := newStyles()
	ta := textarea.New()
	ta.Placeholder = "Send a message… (esc cancels, ctrl-c/q quits)"
	ta.Focus()
	ta.ShowLineNumbers = false

	mode := termmd.ModeStyled
	if opts.NoColor {
		mode = termmd.ModePlain
	}

	m := &Model{
		sess:       sess,
		transcript: newTranscript(st, opts.ShowThoughts),
		input:      ta,
		status:     newStatusLine(st, opts.Title),
		perm:       newPermissionModal(sess, st),
		styles:     st,
		history:    newInputHistory(),
		completion: newCompletionMenu(st),
	}
	m.transcript.SetMode(mode)
	return m
}

// SetSession attaches sess after construction, for callers (the CLI
// bootstrap) that build the Model before Connect returns. Must be called
// before the program starts.
func (m *Model) SetSession(sess *api.Session) {
	m.sess = sess
	m.perm.sess = sess
}

// SeedHistory replays --history events into the transcript before the
// program starts (called by the CLI bootstrap between LoadEvents and
// tea.NewProgram, so historyMsg is never needed as a live message — no
// race with the live event pump, which the bootstrap starts only after
// this seeding completes).
func (m *Model) SeedHistory(events []api.SyncEvent) {
	for _, ev := range events {
		applySyncEvent(m.transcript, ev)
	}
}

// SeedInputHistory replays previously persisted input-history entries
// (oldest first) before the program starts, mirroring SeedHistory's
// ordering discipline so there is no race with a live Add (called only
// from handleKey's enter submit path, which runs after the program starts).
func (m *Model) SeedInputHistory(entries []string) {
	m.history.Seed(entries)
}

// saveHistoryCmd persists the input history to disk as a tea.Cmd — Update
// must stay I/O-free, so this is only ever returned from handleKey, never
// called inline. Errors are swallowed (non-fatal: history degrades to
// in-memory only), matching LoadInputHistory's own non-fatal contract.
func (m *Model) saveHistoryCmd() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	conversationID := m.sess.SessionID()
	entries := append([]string(nil), m.history.entries...)
	return func() tea.Msg {
		_ = SaveInputHistory(conversationID, entries)
		return nil
	}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case eventMsg:
		return m.handleEvent(msg.event)
	case streamEndMsg:
		m.quitting = true
		m.quitErr = msg.err
		m.status.SetDisconnected(msg.err.Error())
		return m, tea.Quit
	case sendDoneMsg:
		if msg.err != nil {
			m.transcript.AppendError(fmt.Sprintf("send failed: %v", msg.err))
		}
		return m, nil
	case cancelDoneMsg:
		if msg.err != nil {
			m.transcript.AppendError(fmt.Sprintf("cancel failed: %v", msg.err))
		}
		return m, nil
	case permAnsweredMsg:
		if msg.err != nil {
			m.transcript.AppendError(fmt.Sprintf("permission answer failed: %v", msg.err))
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	inputHeight := m.input.Height()
	transcriptHeight := m.height - inputHeight - 1 // 1 line for the status bar
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}
	m.transcript.SetSize(m.width, transcriptHeight)
	m.input.SetWidth(m.width)
	m.status.SetWidth(m.width)
	m.perm.SetWidth(m.width)
	m.completion.SetWidth(m.width)
	return m, nil
}

// handleKey routes a key press. Precedence, highest first: the permission
// modal (open ⇒ captures every key, y/n only) > the slash-command
// completion menu (open ⇒ up/down/tab navigate, enter/tab-on-single-match
// accept, esc closes the menu only) > input-history recall (up/down, gated
// to the textarea's first/last line so multi-line editing is unaffected) >
// the textarea itself. This is the split the Plan mandates: the two-stage
// ctrl-c from the readline REPL is dropped as a REPL-only affordance that
// does not carry over to an alt-screen app.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.perm.Open() {
		switch msg.String() {
		case "y":
			return m, m.perm.Answer(true)
		case "n":
			return m, m.perm.Answer(false)
		}
		return m, nil
	}

	if m.completion.Open() {
		if cmd, handled := m.handleCompletionKey(msg); handled {
			return m, cmd
		}
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		return m, m.cancelCmd()
	case "tab":
		if m.maybeOpenCompletion() {
			return m, nil
		}
	case "up":
		if m.input.Line() == 0 {
			if text, ok := m.history.Prev(m.input.Value()); ok {
				m.setInputText(text)
			}
			return m, nil
		}
	case "down":
		if m.input.Line() == m.input.LineCount()-1 {
			if text, ok := m.history.Next(m.input.Value()); ok {
				m.setInputText(text)
			}
			return m, nil
		}
	case "enter":
		text := m.input.Value()
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		m.history.Add(text)
		saveCmd := m.saveHistoryCmd()
		if strings.HasPrefix(text, "/") {
			_, cmd := m.executeSlashCommand(text)
			return m, tea.Batch(cmd, saveCmd)
		}
		m.transcript.AppendUser(text)
		return m, tea.Batch(m.sendCmd(text), saveCmd)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Any ordinary edit abandons an in-progress history recall, mirroring
	// readline (typing while browsing history starts a fresh edit rather
	// than mutating the recalled entry).
	m.history.ResetCursor()
	return m, cmd
}

// maybeOpenCompletion opens the completion menu when the input is a
// single-line value starting with "/", immediately completing when exactly
// one command matches (mirroring mitto cli's completeInput single-match
// convenience). Returns true if tab was consumed (menu opened or
// completed), false if it should fall through to the textarea.
func (m *Model) maybeOpenCompletion() bool {
	value := m.input.Value()
	if m.input.LineCount() != 1 || !strings.HasPrefix(value, "/") {
		return false
	}
	m.completion.Filter(value)
	if !m.completion.Open() {
		return false
	}
	if name, ok := m.completion.SingleMatch(); ok {
		m.setInputText(name)
		m.completion.Close()
	}
	return true
}

// handleCompletionKey handles a key press while the completion menu is
// open. Returns handled=false for any key the menu does not care about, so
// the caller falls through to the normal routing (esc still needs to close
// the menu without triggering the normal esc-cancels-turn behavior, so esc
// IS handled here).
func (m *Model) handleCompletionKey(msg tea.KeyPressMsg) (cmd tea.Cmd, handled bool) {
	switch msg.String() {
	case "up":
		m.completion.Prev()
		return nil, true
	case "down", "tab":
		m.completion.Next()
		return nil, true
	case "enter":
		if name, ok := m.completion.Accept(); ok {
			m.setInputText(name)
		}
		m.completion.Close()
		return nil, true
	case "esc":
		m.completion.Close()
		return nil, true
	}
	return nil, false
}

// setInputText replaces the textarea's value, used by both history recall
// and completion acceptance. SetValue itself already leaves the cursor at
// the end of the inserted text (textarea.Model.InsertString positions the
// cursor there), so no separate cursor move is needed — text may be
// multi-line (a history entry can contain embedded newlines from a
// shift-enter draft), and a column offset computed from len(text) would be
// wrong relative to a single row.
func (m *Model) setInputText(text string) {
	m.input.SetValue(text)
}

// sendCmd issues Session.SendPrompt as a tea.Cmd — no I/O in Update itself.
func (m *Model) sendCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return sendDoneMsg{err: m.sess.SendPrompt(text)}
	}
}

// cancelCmd issues Session.Cancel as a tea.Cmd.
func (m *Model) cancelCmd() tea.Cmd {
	m.inFlight = false
	m.status.SetInFlight(false)
	return func() tea.Msg {
		return cancelDoneMsg{err: m.sess.Cancel()}
	}
}

// handleEvent dispatches a single streamed api.Event into the
// transcript/status line/permission modal, per the Scope in the Plan
// comment: agent messages via termmd, tool calls/thoughts as distinct
// styled blocks, permissions as a modal overlay.
func (m *Model) handleEvent(ev api.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case api.EventConnected:
		m.status.SetConnected(ev.ACPServer)
	case api.EventAgentMessage:
		m.transcript.AppendOrUpdateAgent(ev.Seq, ev.Text, ev.HTML)
	case api.EventAgentThought:
		m.transcript.AppendThought(ev.Text)
	case api.EventToolCall:
		m.transcript.AppendTool(ev.ID, ev.Title, ev.Status)
	case api.EventToolUpdate:
		m.transcript.UpdateTool(ev.ID, ev.Status)
	case api.EventFileRead:
		m.transcript.AppendFileEvent("read", ev.Path, ev.Size)
	case api.EventFileWrite:
		m.transcript.AppendFileEvent("write", ev.Path, ev.Size)
	case api.EventPermission:
		m.perm.Push(ev)
	case api.EventPromptReceived:
		m.inFlight = true
		m.status.SetInFlight(true)
	case api.EventPromptComplete:
		m.inFlight = false
		m.status.SetInFlight(false)
	case api.EventUserPrompt:
		// The server broadcasts user_prompt to every observer including the
		// sender (internal/web/session_ws.go OnUserPrompt: "Always deliver
		// user_prompt to the client"), so our own prompts echo back after
		// handleKey already appended them optimistically. Drop the echo by
		// sender ID, the same dedup the web frontend does with is_mine; a
		// prompt from any other client still renders.
		if id := m.clientID(); id != "" && ev.SenderID == id {
			return m, nil
		}
		m.transcript.AppendUser(ev.Message)
	case api.EventError:
		m.transcript.AppendError(ev.Message)
	case api.EventACPStopped:
		m.status.SetDisconnected("acp stopped: " + ev.Reason)
	case api.EventACPStarted:
		m.status.SetConnected(m.status.acpServer)
	case api.EventSessionGone:
		m.quitting = true
		m.quitErr = fmt.Errorf("conversation was deleted")
		return m, tea.Quit
	}
	return m, nil
}

// clientID returns the server-assigned client ID of our own WebSocket, or
// "" when the session is absent (tests) or the server has not sent the
// connected message yet. Session.ClientID is used rather than the stream's
// EventConnected because the SDK consumes that message during Connect,
// before the event stream is registered — a chat attached to an idle
// conversation would otherwise never learn its own ID.
func (m *Model) clientID() string {
	if m.clientIDFn != nil {
		return m.clientIDFn()
	}
	if m.sess == nil {
		return ""
	}
	return m.sess.ClientID()
}

// QuitErr returns the error that caused the program to quit (nil for a
// normal user-initiated quit). Checked by the CLI command after Run()
// returns to decide the process exit code.
func (m *Model) QuitErr() error { return m.quitErr }

// View renders the full-screen layout: transcript, input textarea, status
// line, with the permission modal overlaid (replacing the input row) when
// open.
func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	bottom := m.input.View()
	if m.completion.Open() {
		bottom = m.completion.Render() + "\n" + bottom
	}
	if m.perm.Open() {
		bottom = m.perm.Render()
	}
	return tea.NewView(m.transcript.View() + "\n" + bottom + "\n" + m.status.Render())
}
