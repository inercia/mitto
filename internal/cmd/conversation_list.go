package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/pkg/api"
)

// listFlags holds the flags for `conversation list`, per the design
// decisions recorded on mitto-pscc.5 (Plan comment).
type listFlags struct {
	Dir       string
	Archived  bool
	Running   bool
	Workspace string
}

var conversationListFlags listFlags

var conversationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List conversations",
	Long: `List conversations known to the server.

Filtering is client-side pending mitto-rwxq.7's ListSessions filter
arguments (docs/devel/cli-conversation.md §2): --dir and --archived filter
the full session list returned by the server locally, and --running
intersects it with the running-sessions list. Archived conversations are
excluded by default (matching the "hidden from main list by default"
convention of session.Metadata.Archived); pass --archived to include them.

--workspace is accepted but currently a no-op with a warning: neither
GET /api/sessions nor its SessionInfo shape carries a workspace UUID.
See mitto-pscc.5.1; use --dir instead until it lands.`,
	Args: cobra.NoArgs,
	RunE: runConversationList,
}

func init() {
	conversationCmd.AddCommand(conversationListCmd)

	f := &conversationListFlags
	conversationListCmd.Flags().StringVar(&f.Dir, "dir", "", "Only show conversations in this working directory")
	conversationListCmd.Flags().BoolVar(&f.Archived, "archived", false, "Also include archived conversations (excluded by default)")
	conversationListCmd.Flags().BoolVar(&f.Running, "running", false, "Only show currently-running conversations")
	conversationListCmd.Flags().StringVar(&f.Workspace, "workspace", "", "Filter by workspace UUID (not yet supported server-side; see mitto-pscc.5.1)")
}

func listTableFn(sessions []api.SessionInfo) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		rows := make([][]string, 0, len(sessions))
		for _, s := range sessions {
			rows = append(rows, []string{s.SessionID, s.Name, s.Status, s.WorkingDir, s.UpdatedAt})
		}
		return []string{"ID", "NAME", "STATUS", "DIR", "UPDATED"}, rows
	}
}

func runConversationList(cmd *cobra.Command, args []string) error {
	if conversationListFlags.Workspace != "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: --workspace is not yet supported (neither GET /api/sessions nor SessionInfo carries a workspace UUID); ignoring. Use --dir instead, or see mitto-pscc.5.1.\n")
	}

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}

	sessions, err := c.ListSessions()
	if err != nil {
		return classify(err)
	}

	var runningIDs map[string]bool
	if conversationListFlags.Running {
		running, rerr := c.ListRunningSessions()
		if rerr != nil {
			return classify(rerr)
		}
		runningIDs = make(map[string]bool, len(running.Sessions))
		for _, r := range running.Sessions {
			runningIDs[r.SessionID] = true
		}
	}

	filtered := make([]api.SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if conversationListFlags.Dir != "" && s.WorkingDir != conversationListFlags.Dir {
			continue
		}
		if !conversationListFlags.Archived && s.Archived {
			continue
		}
		if conversationListFlags.Running && !runningIDs[s.SessionID] {
			continue
		}
		filtered = append(filtered, s)
	}

	return emit(cmd, &conversationFlags, filtered, listTableFn(filtered))
}
