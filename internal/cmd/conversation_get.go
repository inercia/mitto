package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	client "github.com/inercia/mitto/pkg/api"
)

// conversationDetails composes a conversation's REST resources that have no
// single response shape into one CLI-owned struct, per the documented
// "SDK response types directly" exception in docs/devel/cli-conversation.md
// §4 (the same exception conversation_wait.go's waitResult already takes):
// the session info, its loop configuration (nil if none configured), and
// its current queue depth.
type conversationDetails struct {
	Session    *client.SessionInfo `json:"session"`
	Loop       *client.LoopConfig  `json:"loop,omitempty"`
	QueueDepth int                 `json:"queue_depth"`
}

var conversationGetCmd = &cobra.Command{
	Use:   "get <conversation-id>",
	Short: "Show conversation details",
	Long: `Show a conversation's full details: its session info, loop configuration
(if any), and current queue depth.

Not found (404) on the session itself maps to exit 5. A missing loop
configuration is not an error — it is a normal state rendered as
"LOOP ENABLED: not configured" (table) or an omitted "loop" field
(json/yaml).`,
	Args: cobra.ExactArgs(1),
	RunE: runConversationGet,
}

func init() {
	conversationCmd.AddCommand(conversationGetCmd)
}

func getTableFn(d *conversationDetails) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		loopEnabled := "not configured"
		if d.Loop != nil {
			loopEnabled = fmt.Sprintf("%t", d.Loop.Enabled)
		}
		rows := [][]string{
			{"ID", d.Session.SessionID},
			{"NAME", d.Session.Name},
			{"STATUS", d.Session.Status},
			{"DIR", d.Session.WorkingDir},
			{"ACP", d.Session.ACPServer},
			{"CREATED", d.Session.CreatedAt},
			{"UPDATED", d.Session.UpdatedAt},
			{"QUEUE DEPTH", fmt.Sprintf("%d", d.QueueDepth)},
			{"LOOP ENABLED", loopEnabled},
		}
		return []string{"FIELD", "VALUE"}, rows
	}
}

func runConversationGet(cmd *cobra.Command, args []string) error {
	conversationID := args[0]

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}

	info, err := c.GetSession(conversationID)
	if err != nil {
		return classify(err)
	}

	details := &conversationDetails{Session: info}

	// The session is confirmed to exist above, so a 404 here can only mean
	// "loop not configured" (client.GetLoop's synthetic ErrNotFound) — swallow
	// it to a nil Loop rather than surfacing exit 5 for a normal, common state.
	loop, lerr := c.GetLoop(conversationID)
	switch {
	case lerr == nil:
		details.Loop = loop
	case errors.Is(lerr, client.ErrNotFound):
		// not configured; leave Loop nil
	default:
		return classify(lerr)
	}

	q, qerr := c.ListQueue(conversationID)
	if qerr != nil {
		return classify(qerr)
	}
	details.QueueDepth = q.Count

	return emit(cmd, &conversationFlags, details, getTableFn(details))
}
