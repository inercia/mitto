package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/pkg/api"
)

// newFlags holds the flags for `conversation new`, per the design decisions
// recorded on mitto-pscc.5 (Plan comment).
type newFlags struct {
	Title       string
	Dir         string
	ACP         string
	Prompt      string
	PromptName  string
	Args        []string
	Wait        bool
	WaitTimeout time.Duration
}

var conversationNewFlags newFlags

var conversationNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new conversation",
	Long: `Create a new conversation on the server, optionally seeding it with an
initial prompt.

At most one of --prompt (free text; "-" reads stdin) or --prompt-name (a
named workspace prompt, with --arg key=value for its arguments) may be
given. Without --wait, a --prompt-name is seeded atomically at creation
(the same queue plumbing POST /api/sessions/{id}/queue uses) and a --prompt
is enqueued in a second REST call right after creation. With --wait, the
command always defers seeding until AFTER the WebSocket is connected and
this client is registered as an observer (docs/devel/cli-conversation.md
§8) — the atomic at-creation seed dispatches immediately
(seedQueueWithNamedPrompt), which could otherwise finish the whole turn
before a post-creation Connect ever happens.`,
	Args: cobra.NoArgs,
	RunE: runConversationNew,
}

func init() {
	conversationCmd.AddCommand(conversationNewCmd)

	f := &conversationNewFlags
	conversationNewCmd.Flags().StringVar(&f.Title, "title", "", "Conversation title/name")
	conversationNewCmd.Flags().StringVar(&f.Dir, "dir", "", "Working directory for the conversation (default: current directory)")
	conversationNewCmd.Flags().StringVar(&f.ACP, "acp", "", "ACP server to use")
	conversationNewCmd.Flags().StringVar(&f.Prompt, "prompt", "", `Initial prompt text (alternative to --prompt-name; "-" reads stdin)`)
	conversationNewCmd.Flags().StringVar(&f.PromptName, "prompt-name", "", "Seed the queue with a named workspace prompt instead of free text")
	conversationNewCmd.Flags().StringArrayVar(&f.Args, "arg", nil, "key=value argument for --prompt-name (repeatable)")
	conversationNewCmd.Flags().BoolVar(&f.Wait, "wait", false, "Wait for the agent to finish responding to the initial prompt")
	conversationNewCmd.Flags().DurationVar(&f.WaitTimeout, "wait-timeout", 10*time.Minute, "Maximum time to wait with --wait (0 = no limit); a timeout never cancels the agent")
}

func newTableFn(info *api.SessionInfo) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		return []string{"ID", "NAME", "DIR", "ACP", "REUSED"},
			[][]string{{info.SessionID, info.Name, info.WorkingDir, info.ACPServer, fmt.Sprintf("%t", info.Reused)}}
	}
}

func runConversationNew(cmd *cobra.Command, args []string) error {
	promptChanged := cmd.Flags().Changed("prompt")
	promptNameChanged := cmd.Flags().Changed("prompt-name")
	if promptChanged && promptNameChanged {
		return newExitCodeError(exitUsage, fmt.Errorf("--prompt and --prompt-name are mutually exclusive"))
	}
	if len(conversationNewFlags.Args) > 0 && !promptNameChanged {
		return newExitCodeError(exitUsage, fmt.Errorf("--arg requires --prompt-name"))
	}
	if conversationNewFlags.Wait && !promptChanged && !promptNameChanged {
		return newExitCodeError(exitUsage, fmt.Errorf("--wait requires --prompt or --prompt-name"))
	}

	promptArgs, err := parseSendArgs(conversationNewFlags.Args)
	if err != nil {
		return newExitCodeError(exitUsage, err)
	}

	promptText := conversationNewFlags.Prompt
	if promptChanged && promptText == "-" {
		data, rerr := io.ReadAll(cmd.InOrStdin())
		if rerr != nil {
			return fmt.Errorf("reading prompt text from stdin: %w", rerr)
		}
		promptText = string(data)
	}

	dir := conversationNewFlags.Dir
	if dir == "" {
		if wd, werr := os.Getwd(); werr == nil {
			dir = wd
		}
	}

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}

	req := api.CreateSessionRequest{
		Name:       conversationNewFlags.Title,
		WorkingDir: dir,
		ACPServer:  conversationNewFlags.ACP,
	}
	if promptNameChanged && !conversationNewFlags.Wait {
		req.InitialPromptName = conversationNewFlags.PromptName
		req.Arguments = promptArgs
	}

	info, err := c.CreateSession(req)
	if err != nil {
		return classify(err)
	}

	if !conversationNewFlags.Wait {
		if promptChanged {
			if _, err := enqueueSend(c, info.SessionID, false, "", nil, promptText, nil); err != nil {
				return classify(err)
			}
		}
		return emit(cmd, &conversationFlags, info, newTableFn(info))
	}

	waitCtx := context.Background()
	var waitCancel context.CancelFunc
	if conversationNewFlags.WaitTimeout > 0 {
		waitCtx, waitCancel = context.WithTimeout(waitCtx, conversationNewFlags.WaitTimeout)
	} else {
		waitCtx, waitCancel = context.WithCancel(waitCtx)
	}
	defer waitCancel()

	sess, gate, evCh, errCh, err := connectAndAwaitLoad(waitCtx, c, info.SessionID)
	if err != nil {
		return classify(err)
	}
	defer sess.Close()

	queued, err := enqueueSend(c, info.SessionID, promptNameChanged, conversationNewFlags.PromptName, promptArgs, promptText, nil)
	if err != nil {
		return classify(err)
	}
	gate.setWant(queued.ID)

	format, ferr := parseOutputFormat(conversationFlags.Output)
	if ferr != nil {
		return ferr
	}
	streamText := format == outputTable

	result, werr := waitForQueuedMessage(waitCtx, evCh, errCh, gate, streamText, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if werr != nil {
		return classify(werr)
	}
	result.Queued = queued

	if streamText {
		return nil
	}
	return emit(cmd, &conversationFlags, result, func() ([]string, [][]string) { return nil, nil })
}
