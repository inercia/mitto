package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var authRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Rotate the shared bearer token",
	Long: `Rotate the shared bearer token on a running Mitto server.

Rotation is server-side: the server generates a new token, installs it on
its running auth manager, and rewrites instance.json — a CLI-only rewrite of
instance.json would leave the live server still validating the old token.
This command only reaches the server's LOOPBACK listener (the server
rejects the request outright if it arrives through the external listener),
so it works even when authentication is not otherwise configured.

Only a token adopted from instance.json can be rotated this way. If the
shared token was configured explicitly (MITTO_SHARED_TOKEN, settings.json,
or the system keychain), this command refuses — rotating an
operator-managed secret is out of scope; update it at its source instead.

Every client holding the previous token (other CLI shells, SDK clients,
MITTO_TOKEN exports) is rejected immediately once this command succeeds and
must re-read instance.json (or otherwise obtain the new token).`,
	Args: cobra.NoArgs,
	RunE: runAuthRotate,
}

func init() {
	authCmd.AddCommand(authRotateCmd)
}

func runAuthRotate(cmd *cobra.Command, args []string) error {
	c, err := newClient(&authFlags)
	if err != nil {
		return err
	}

	result, rerr := c.RotateSharedToken()
	if rerr != nil {
		return classify(rerr)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "warning: every client holding the previous token (other CLI shells, SDK clients, MITTO_TOKEN exports) is now rejected and must re-read instance.json.\n")

	return emit(cmd, &authFlags, result, func() ([]string, [][]string) {
		return []string{"NEW TOKEN FINGERPRINT"}, [][]string{{result.Fingerprint}}
	})
}
