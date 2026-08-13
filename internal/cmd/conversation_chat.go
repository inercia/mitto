package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/chatui"
	"github.com/inercia/mitto/internal/termmd"
	"github.com/inercia/mitto/pkg/api"
)

// chatFlags holds the flags for `conversation chat`, per the design
// decisions recorded on mitto-pscc.7's Plan comment and
// docs/devel/cli-conversation.md §7.
type chatFlags struct {
	History    int64
	NoThoughts bool
}

var conversationChatFlags chatFlags

var conversationChatCmd = &cobra.Command{
	Use:   "chat [conversation-id]",
	Short: "Open an interactive full-screen chat TUI over WebSocket",
	Long: `Attach a full-screen Bubble Tea terminal UI to an existing conversation.

When conversation-id is omitted, choose from a recent-first interactive list.
Archived conversations and automatically-created children are hidden from the
picker; human-created and MCP-created conversations remain selectable.

Requires stdout and stdin to both be interactive terminals: piped or
redirected I/O exits with a usage error pointing at "conversation send
--wait" instead. --history N replays the N most recent events into the
transcript on attach. esc cancels the in-flight agent turn; ctrl-c/q quits.

Up/down recall previously submitted lines (persisted per conversation);
tab completes slash commands (/help, /quit, /cancel, /clear).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runConversationChat,
}

func init() {
	conversationCmd.AddCommand(conversationChatCmd)

	f := &conversationChatFlags
	conversationChatCmd.Flags().Int64Var(&f.History, "history", 20, "Number of recent events to replay into the transcript on attach (0 = none)")
	conversationChatCmd.Flags().BoolVar(&f.NoThoughts, "no-thoughts", false, "Hide agent thinking/thought events")
}

// resolveChatStyle applies the flag > $GLAMOUR_STYLE steps of termmd.
// ResolveTheme's precedence (mitto-u7k3) before the chat TUI even starts:
// an explicit "dark"/"light" (from --style or $GLAMOUR_STYLE) is returned
// as-is; anything else becomes "auto", handing background detection to
// chatui.Model's tea.RequestBackgroundColor flow (Init/Update) since a
// raw lipgloss query would fight Bubble Tea's own input reader for the
// terminal.
func resolveChatStyle(styleFlag string) string {
	if styleFlag == "dark" || styleFlag == "light" {
		return styleFlag
	}
	if env := os.Getenv("GLAMOUR_STYLE"); env == "dark" || env == "light" {
		return env
	}
	return "auto"
}

func resolveChatPresentation(noColor bool, style string) chatui.PresentationOptions {
	return chatui.PresentationOptions{
		NoColor: termmd.ResolveMode(noColor) == termmd.ModePlain,
		Style:   resolveChatStyle(style),
	}
}

func runConversationChat(cmd *cobra.Command, args []string) error {
	// TTY precondition (Plan Scope: "this command requires a TTY"). Checked
	// before any dialing so a piped invocation fails fast with a usage
	// error rather than hanging in an alt-screen program nobody can see.
	if !stdinIsTerminal() || !termmd.StdoutIsTerminal() {
		return newExitCodeError(exitUsage, fmt.Errorf(
			`"conversation chat" requires an interactive terminal on both stdin and stdout; use "conversation send --wait" for non-interactive/piped use`))
	}

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}
	presentation := resolveChatPresentation(conversationFlags.NoColor, conversationFlags.Style)
	picker := func(sessions []api.SessionInfo) (string, bool, error) {
		return pickChatConversation(sessions, presentation)
	}
	conversationID, selected, err := resolveChatConversationID(args, c.ListSessions, picker)
	if err != nil {
		return classify(err)
	}
	if !selected {
		return nil
	}

	return runConversationChatForID(c, conversationID, presentation)
}

type chatConversationPicker func([]api.SessionInfo) (string, bool, error)

func resolveChatConversationID(
	args []string,
	listSessions func() ([]api.SessionInfo, error),
	pick chatConversationPicker,
) (string, bool, error) {
	if len(args) == 1 {
		return args[0], true, nil
	}
	sessions, err := listSessions()
	if err != nil {
		return "", false, err
	}
	candidates := selectableChatSessions(sessions)
	if len(candidates) == 0 {
		return "", false, fmt.Errorf("no selectable conversations found (archived and automatic child conversations are hidden)")
	}
	return pick(candidates)
}

func selectableChatSessions(sessions []api.SessionInfo) []api.SessionInfo {
	filtered := make([]api.SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if session.Archived || session.ChildOrigin == "auto" {
			continue
		}
		filtered = append(filtered, session)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		iTime, iOK := chatSessionUpdatedAt(filtered[i].UpdatedAt)
		jTime, jOK := chatSessionUpdatedAt(filtered[j].UpdatedAt)
		if iOK != jOK {
			return iOK
		}
		return iOK && iTime.After(jTime)
	})
	return filtered
}

func chatSessionUpdatedAt(value string) (time.Time, bool) {
	updated, err := time.Parse(time.RFC3339Nano, value)
	return updated, err == nil
}

func pickChatConversation(sessions []api.SessionInfo, presentation chatui.PresentationOptions) (string, bool, error) {
	model := chatui.NewSessionPickerModel(sessions, presentation)
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return "", false, err
	}
	result, ok := finalModel.(*chatui.SessionPickerModel)
	if !ok {
		return "", false, fmt.Errorf("session picker returned unexpected model %T", finalModel)
	}
	if result.Cancelled() {
		return "", false, nil
	}
	if result.SelectedSessionID() == "" {
		return "", false, fmt.Errorf("session picker exited without selecting a conversation")
	}
	return result.SelectedSessionID(), true, nil
}

func runConversationChatForID(c *api.Client, conversationID string, presentation chatui.PresentationOptions) error {
	info, err := c.GetSession(conversationID)
	if err != nil {
		return classify(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	title := info.Name
	if title == "" {
		title = conversationID
	}
	model := chatui.NewModel(nil, chatui.Options{
		Title:        title,
		NoColor:      presentation.NoColor,
		Style:        presentation.Style,
		ShowThoughts: !conversationChatFlags.NoThoughts,
	})

	// Seed persisted input history before the program starts (mitto-pscc.11),
	// mirroring SeedHistory's ordering discipline. A missing/unreadable file
	// is non-fatal — history just starts empty for this run.
	if entries, herr := chatui.LoadInputHistory(conversationID); herr == nil {
		model.SeedInputHistory(entries)
	}

	// Bootstrap mirrors connectAndAwaitLoad (conversation_send.go), with the
	// history limit driving the same LoadEvents/OnEventsLoaded round trip
	// instead of the fixed limit=1 --wait uses (docs/devel/
	// cli-conversation.md §8: a bare Connect() receives nothing until this
	// client registers as an observer via load_events).
	loaded := make(chan struct{})
	var loadedOnce sync.Once
	sess, err := c.Connect(ctx, conversationID, api.SessionCallbacks{
		OnEventsLoaded: func(events []api.SyncEvent, hasMore bool, isPrompting bool) {
			model.SeedHistory(events)
			loadedOnce.Do(func() { close(loaded) })
		},
	})
	if err != nil {
		return classify(err)
	}
	defer sess.Close()
	model.SetSession(sess)

	evCh, errCh, err := sess.EventsChan(ctx)
	if err != nil {
		return classify(err)
	}

	if err := sess.LoadEvents(conversationChatFlags.History, 0, 0); err != nil {
		return classify(err)
	}
	select {
	case <-loaded:
	case <-ctx.Done():
		return classify(ctx.Err())
	}

	program := tea.NewProgram(model, tea.WithContext(ctx))
	go chatui.RunPump(ctx, evCh, errCh, program)

	finalModel, runErr := program.Run()
	cancel()
	if runErr != nil {
		return classify(runErr)
	}
	if m, ok := finalModel.(*chatui.Model); ok {
		if qerr := m.QuitErr(); qerr != nil {
			return newExitCodeError(exitGeneric, qerr)
		}
	}
	return nil
}
