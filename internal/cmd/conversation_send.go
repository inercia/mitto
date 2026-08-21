package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/pkg/api"
)

// sendFlags holds the flags for `conversation send`, per the design decisions
// recorded on mitto-pscc.6 (Plan comment) and docs/devel/cli-conversation.md.
type sendFlags struct {
	Prompt      string
	PromptName  string
	Args        []string
	Images      []string
	Wait        bool
	WaitTimeout time.Duration
}

var conversationSendFlags sendFlags

var conversationSendCmd = &cobra.Command{
	Use:   "send <conversation-id> [text]",
	Short: "Enqueue a prompt for a conversation, optionally waiting for the reply",
	Long: `Enqueue a message on a conversation's REST queue (the same durable
queue the frontend uses); an idle session starts processing it immediately.

The body is resolved from exactly one source: the positional text, --prompt,
or --prompt-name. A body of "-" (positional or --prompt) reads the full
prompt text from stdin.

With --wait, the command connects a WebSocket before enqueuing (to avoid a
race where an idle session finishes the turn before the socket is attached),
then streams the agent's reply to stdout until this specific message's turn
completes. --wait-timeout bounds how long the CLI waits; the agent keeps
running server-side even if the wait times out.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runConversationSend,
}

func init() {
	conversationCmd.AddCommand(conversationSendCmd)

	f := &conversationSendFlags
	conversationSendCmd.Flags().StringVar(&f.Prompt, "prompt", "", `Prompt text (alternative to positional text; "-" reads stdin)`)
	conversationSendCmd.Flags().StringVar(&f.PromptName, "prompt-name", "", "Enqueue a named workspace prompt instead of free text")
	conversationSendCmd.Flags().StringArrayVar(&f.Args, "arg", nil, "key=value argument for --prompt-name (repeatable)")
	conversationSendCmd.Flags().StringArrayVar(&f.Images, "image", nil, "Path to an image to attach (repeatable; not combinable with --prompt-name)")
	conversationSendCmd.Flags().BoolVar(&f.Wait, "wait", false, "Wait for the agent to finish responding to this message")
	conversationSendCmd.Flags().DurationVar(&f.WaitTimeout, "wait-timeout", 10*time.Minute, "Maximum time to wait with --wait (0 = no limit); a timeout never cancels the agent")
}

// resolveSendBody validates the body/--arg/--image flag combination and
// resolves exactly one body source, per the Plan decisions on mitto-pscc.6.
// It returns either (text, false, "", nil) for free text, or
// ("", true, promptName, args) for a named prompt.
func resolveSendBody(cmd *cobra.Command, args []string) (text string, usingPromptName bool, promptName string, promptArgs map[string]string, err error) {
	hasPositional := len(args) > 1
	promptChanged := cmd.Flags().Changed("prompt")
	promptNameChanged := cmd.Flags().Changed("prompt-name")

	sources := 0
	if hasPositional {
		sources++
	}
	if promptChanged {
		sources++
	}
	if promptNameChanged {
		sources++
	}
	if sources != 1 {
		return "", false, "", nil, newExitCodeError(exitUsage,
			fmt.Errorf("exactly one of positional text, --prompt, or --prompt-name is required"))
	}

	if len(conversationSendFlags.Args) > 0 && !promptNameChanged {
		return "", false, "", nil, newExitCodeError(exitUsage,
			fmt.Errorf("--arg requires --prompt-name"))
	}
	if len(conversationSendFlags.Images) > 0 && promptNameChanged {
		return "", false, "", nil, newExitCodeError(exitUsage,
			fmt.Errorf("--image cannot be combined with --prompt-name"))
	}

	if promptNameChanged {
		argsMap, aerr := parseSendArgs(conversationSendFlags.Args)
		if aerr != nil {
			return "", false, "", nil, newExitCodeError(exitUsage, aerr)
		}
		return "", true, conversationSendFlags.PromptName, argsMap, nil
	}

	raw := conversationSendFlags.Prompt
	if hasPositional {
		raw = args[1]
	}
	if raw == "-" {
		data, rerr := io.ReadAll(cmd.InOrStdin())
		if rerr != nil {
			return "", false, "", nil, fmt.Errorf("reading prompt text from stdin: %w", rerr)
		}
		raw = string(data)
	}
	return raw, false, "", nil, nil
}

// parseSendArgs parses repeated key=value pairs into a map. A malformed
// entry (no "=", or an empty key) is a usage error.
func parseSendArgs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --arg %q: must be key=value", p)
		}
		out[k] = v
	}
	return out, nil
}

// uploadSendImages uploads each path in order and returns their server-side
// image IDs, in the same order, for attaching to the queued message.
func uploadSendImages(c *api.Client, conversationID string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading image %q: %w", p, err)
		}
		info, err := c.UploadImage(conversationID, filepath.Base(p), http.DetectContentType(data), data)
		if err != nil {
			return nil, err
		}
		ids = append(ids, info.ID)
	}
	return ids, nil
}

// enqueueSend performs the single REST enqueue call matching the resolved
// body source.
func enqueueSend(c *api.Client, conversationID string, usingPromptName bool, promptName string, promptArgs map[string]string, text string, imageIDs []string) (*api.QueuedMessage, error) {
	if usingPromptName {
		return c.AddToQueueNamedWithArgs(conversationID, promptName, promptArgs)
	}
	if len(imageIDs) > 0 {
		return c.AddToQueueWithImages(conversationID, text, imageIDs)
	}
	return c.AddToQueue(conversationID, text)
}

// connectAndAwaitLoad connects to conversationID, registers this client's
// event stream, and blocks until the "events_loaded" handshake completes
// before returning — the ordering documented in
// docs/devel/cli-conversation.md §8, extracted here so both `conversation
// send --wait` and `conversation new --wait` (mitto-pscc.5) share exactly
// one implementation:
//
//   - Connect (and register the event stream) BEFORE enqueuing: the REST
//     enqueue triggers immediate processing on an idle session
//     (internal/web/handlers/queue.go), which could otherwise complete the
//     whole turn before a post-enqueue WebSocket dial lands.
//   - A bare Connect() does not make this client an observer of the
//     session: the server only calls BackgroundSession.AddObserver from its
//     load_events handler (internal/web/session_ws.go postLoadProcessing),
//     which fires after an events_loaded reply. Without this handshake,
//     agent_message/prompt_complete/queue_message_sending notifications for
//     OUR message would never reach evCh/gate, hanging --wait until
//     --wait-timeout regardless of whether the turn actually completes.
//     limit=1 keeps the (unused) historical replay minimal; the events it
//     may carry are not modelled by the Event stream and are silently
//     ignored (see eventFromMessage). Waiting for events_loaded before
//     enqueuing (rather than racing them) also preserves this ordering
//     guarantee: server-side AddObserver happens synchronously right after
//     events_loaded is queued for delivery, so receiving it here is a
//     reliable (if not formally atomic) signal that registration has
//     already happened by the time our REST enqueue reaches the server over
//     its own, later network round trip.
//
// On success the caller owns the returned *api.Session and must Close()
// it. On error, any partially-established connection is already closed
// internally — the caller has nothing to clean up.
func connectAndAwaitLoad(waitCtx context.Context, c *api.Client, conversationID string) (*api.Session, *queueGate, <-chan api.Event, <-chan error, error) {
	gate := newQueueGate()
	loaded := make(chan struct{})
	var loadedOnce sync.Once
	sess, err := c.Connect(waitCtx, conversationID, api.SessionCallbacks{
		OnQueueMessageSending: gate.onQueueMessageSending,
		OnEventsLoaded: func(events []api.SyncEvent, hasMore bool, isPrompting bool) {
			loadedOnce.Do(func() { close(loaded) })
		},
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	evCh, errCh, err := sess.EventsChan(waitCtx)
	if err != nil {
		sess.Close()
		return nil, nil, nil, nil, err
	}

	if err := sess.LoadEvents(1, 0, 0); err != nil {
		sess.Close()
		return nil, nil, nil, nil, err
	}
	select {
	case <-loaded:
	case <-waitCtx.Done():
		sess.Close()
		return nil, nil, nil, nil, waitCtx.Err()
	}

	return sess, gate, evCh, errCh, nil
}

func sendTableFn(queued *api.QueuedMessage) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		return []string{"ID", "QUEUED AT", "TITLE"}, [][]string{{queued.ID, queued.QueuedAt, queued.Title}}
	}
}

func runConversationSend(cmd *cobra.Command, args []string) error {
	conversationID := args[0]

	text, usingPromptName, promptName, promptArgs, err := resolveSendBody(cmd, args)
	if err != nil {
		return err
	}

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}

	var (
		gate  *queueGate
		evCh  <-chan api.Event
		errCh <-chan error
	)

	waitCtx := context.Background()
	if conversationSendFlags.Wait {
		var waitCancel context.CancelFunc
		if conversationSendFlags.WaitTimeout > 0 {
			waitCtx, waitCancel = context.WithTimeout(waitCtx, conversationSendFlags.WaitTimeout)
		} else {
			waitCtx, waitCancel = context.WithCancel(waitCtx)
		}
		defer waitCancel()

		var sess *api.Session
		sess, gate, evCh, errCh, err = connectAndAwaitLoad(waitCtx, c, conversationID)
		if err != nil {
			return classify(err)
		}
		defer sess.Close()
	}

	imageIDs, err := uploadSendImages(c, conversationID, conversationSendFlags.Images)
	if err != nil {
		return classify(err)
	}

	queued, err := enqueueSend(c, conversationID, usingPromptName, promptName, promptArgs, text, imageIDs)
	if err != nil {
		return classify(err)
	}

	if !conversationSendFlags.Wait {
		return emit(cmd, &conversationFlags, queued, sendTableFn(queued))
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
		return nil // the message body was already streamed to stdout
	}
	return emit(cmd, &conversationFlags, result, func() ([]string, [][]string) { return nil, nil })
}
