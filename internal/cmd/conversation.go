package cmd

import (
	"time"

	"github.com/spf13/cobra"
)

// serverFlags holds the persistent flags shared by the conversation and auth
// command trees (docs/devel/cli-conversation.md §2). Both parents bind their
// own instance of the underlying vars via identical flag registrations below;
// resolution (flag > env > instance.json) happens in resolveTarget.
type serverFlags struct {
	URL       string
	Token     string
	APIPrefix string
	Timeout   time.Duration
	Output    string
	NoColor   bool
	Style     string
}

// conversationFlags and authFlags are separate instances (not shared) since
// cobra persistent flags are bound per-command-tree; a user invoking
// `mitto conversation ...` or `mitto auth ...` only ever populates one.
var (
	conversationFlags serverFlags
	authFlags         serverFlags
)

// conversationCmd is the parent command for all conversation-related
// subcommands (new/list/get/delete/send/chat). It has no RunE of its own.
var conversationCmd = &cobra.Command{
	Use:   "conversation",
	Short: "Manage conversations on a running Mitto server",
	Long: `Manage conversations on a running Mitto server over its REST API.

Unlike "mitto cli" (which spawns an ACP agent directly), these commands talk
to an already-running "mitto web" server (or the macOS app), discovered via
--url/--token, MITTO_URL/MITTO_TOKEN, or the local instance.json file.`,
}

// authCmd is the parent command for auth-related subcommands (status/rotate).
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect and manage authentication for a running Mitto server",
}

func init() {
	rootCmd.AddCommand(conversationCmd)
	rootCmd.AddCommand(authCmd)

	registerServerFlags(conversationCmd, &conversationFlags)
	registerServerFlags(authCmd, &authFlags)
}

// registerServerFlags registers the shared --url/--token/--api-prefix/
// --timeout/--output/--no-color persistent flags on cmd, binding them to f.
func registerServerFlags(cmd *cobra.Command, f *serverFlags) {
	cmd.PersistentFlags().StringVar(&f.URL, "url", "", "Mitto server base URL (default: $MITTO_URL, then instance.json)")
	cmd.PersistentFlags().StringVar(&f.Token, "token", "", "Bearer token for authentication (default: $MITTO_TOKEN, then instance.json)")
	cmd.PersistentFlags().StringVar(&f.APIPrefix, "api-prefix", "", "API path prefix (default: $MITTO_API_PREFIX, then instance.json; only \"/mitto\" is currently supported)")
	cmd.PersistentFlags().DurationVar(&f.Timeout, "timeout", 30*time.Second, "HTTP request timeout")
	cmd.PersistentFlags().StringVar(&f.Output, "output", "table", "Output format: table, json, or yaml")
	cmd.PersistentFlags().BoolVar(&f.NoColor, "no-color", false, "Disable colored/styled output (also: $NO_COLOR)")
	cmd.PersistentFlags().StringVar(&f.Style, "style", "auto", "Styled-mode color palette: auto, dark, or light (also: $GLAMOUR_STYLE)")
}
