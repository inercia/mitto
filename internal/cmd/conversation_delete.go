package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// deleteFlags holds the flags for `conversation delete`.
type deleteFlags struct {
	Force bool
}

var conversationDeleteFlags deleteFlags

var conversationDeleteCmd = &cobra.Command{
	Use:   "delete <conversation-id>",
	Short: "Delete a conversation",
	Long: `Permanently delete a conversation from the server.

Without --force, asks for confirmation (y/N, on stderr) when stdin is an
interactive terminal. On a non-TTY stdin, refuses with exit 2 rather than
hanging on a read that will never receive input — pass --force instead.`,
	Args: cobra.ExactArgs(1),
	RunE: runConversationDelete,
}

func init() {
	conversationCmd.AddCommand(conversationDeleteCmd)

	conversationDeleteCmd.Flags().BoolVarP(&conversationDeleteFlags.Force, "force", "f", false, "Skip the confirmation prompt")
}

// deleteResult is the --output json/yaml result for `delete`, per the same
// documented compose-a-struct exception `get` and `send --wait` use:
// DELETE /api/sessions/{id} has no response body to marshal.
type deleteResult struct {
	SessionID string `json:"session_id"`
	Deleted   bool   `json:"deleted"`
}

// stdinIsTerminal reports whether os.Stdin is an interactive terminal, via a
// raw os.ModeCharDevice check — deliberately not adding golang.org/x/term or
// promoting the already-indirect mattn/go-isatty dependency for this single
// check (Plan decision, mitto-pscc.5).
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// confirmDelete prompts on stderr and reads a y/N answer from cmd's stdin.
func confirmDelete(cmd *cobra.Command, conversationID string) (bool, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "Delete conversation %s? [y/N]: ", conversationID)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func runConversationDelete(cmd *cobra.Command, args []string) error {
	conversationID := args[0]

	if !conversationDeleteFlags.Force {
		if !stdinIsTerminal() {
			return newExitCodeError(exitUsage, fmt.Errorf(
				"refusing to delete %q without confirmation on a non-interactive stdin; pass --force to skip the prompt", conversationID))
		}
		ok, cerr := confirmDelete(cmd, conversationID)
		if cerr != nil {
			return fmt.Errorf("reading confirmation: %w", cerr)
		}
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}

	c, err := newClient(&conversationFlags)
	if err != nil {
		return err
	}

	if err := c.DeleteSession(conversationID); err != nil {
		return classify(err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "deleted %s\n", conversationID)

	format, ferr := parseOutputFormat(conversationFlags.Output)
	if ferr != nil {
		return ferr
	}
	if format == outputTable {
		return nil
	}
	result := deleteResult{SessionID: conversationID, Deleted: true}
	return emit(cmd, &conversationFlags, result, func() ([]string, [][]string) { return nil, nil })
}
