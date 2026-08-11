package cmd

import (
	"context"
	"fmt"
	"sync"

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
	Use:   "chat <conversation-id>",
	Short: "Open an interactive full-screen chat TUI over WebSocket",
	Long: `Attach a full-screen Bubble Tea terminal UI to an existing conversation.

Requires stdout and stdin to both be interactive terminals: piped or
redirected I/O exits with a usage error pointing at "conversation send
--wait" instead. --history N replays the N most recent events into the
transcript on attach. esc cancels the in-flight agent turn; ctrl-c/q quits.

Up/down recall previously submitted lines (persisted per conversation);
tab completes slash commands (/help, /quit, /cancel, /clear).`,
	Args: cobra.ExactArgs(1),
	RunE: runConversationChat,
}

func init() {
	conversationCmd.AddCommand(conversationChatCmd)

	f := &conversationChatFlags
	conversationChatCmd.Flags().Int64Var(&f.History, "history", 20, "Number of recent events to replay into the transcript on attach (0 = none)")
	conversationChatCmd.Flags().BoolVar(&f.NoThoughts, "no-thoughts", false, "Hide agent thinking/thought events")
}

func runConversationChat(cmd *cobra.Command, args []string) error {
	conversationID := args[0]

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
		NoColor:      conversationFlags.NoColor,
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
