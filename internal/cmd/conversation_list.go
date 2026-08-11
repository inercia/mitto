package cmd

import (
	"strings"

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

--workspace matches a session's server-derived workspace UUID (exact,
case-sensitive) or workspace name (case-insensitive), also client-side.
A value matching no configured workspace at all yields an empty result
rather than an error, since the SDK has no workspaces list to validate
against (mitto-pscc.5.1).`,
	Args: cobra.NoArgs,
	RunE: runConversationList,
}

func init() {
	conversationCmd.AddCommand(conversationListCmd)

	f := &conversationListFlags
	conversationListCmd.Flags().StringVar(&f.Dir, "dir", "", "Only show conversations in this working directory")
	conversationListCmd.Flags().BoolVar(&f.Archived, "archived", false, "Also include archived conversations (excluded by default)")
	conversationListCmd.Flags().BoolVar(&f.Running, "running", false, "Only show currently-running conversations")
	conversationListCmd.Flags().StringVar(&f.Workspace, "workspace", "", "Filter by workspace UUID or name")
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
		if w := conversationListFlags.Workspace; w != "" {
			if s.WorkspaceUUID != w && !strings.EqualFold(s.WorkspaceName, w) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	return emit(cmd, &conversationFlags, filtered, listTableFn(filtered))
}
