package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/instancefile"
)

// authStatus is the CLI-owned output struct for `auth status` (no single
// REST response shape covers resolved-target provenance + reachability +
// auth-mode — same documented exception as conversation get's
// conversationDetails, docs/devel/cli-conversation.md §4). Never carries the
// token itself, only a non-reversible fingerprint (instancefile.Fingerprint).
type authStatus struct {
	URL              string `json:"url"`
	APIPrefix        string `json:"api_prefix"`
	ExternalURL      string `json:"external_url,omitempty"`
	PID              int    `json:"pid,omitempty"`
	Reachable        bool   `json:"reachable"`
	AuthEnabled      bool   `json:"auth_enabled"`
	Cloudflare       bool   `json:"cloudflare"`
	TokenSource      string `json:"token_source"`
	TokenFingerprint string `json:"token_fingerprint,omitempty"`
}

func statusTableFn(s *authStatus) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		return []string{"URL", "API PREFIX", "REACHABLE", "AUTH ENABLED", "CLOUDFLARE", "TOKEN SOURCE", "TOKEN FINGERPRINT"},
			[][]string{{s.URL, s.APIPrefix, fmt.Sprintf("%v", s.Reachable), fmt.Sprintf("%v", s.AuthEnabled), fmt.Sprintf("%v", s.Cloudflare), s.TokenSource, s.TokenFingerprint}}
	}
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Inspect the resolved server address and shared token",
	Long: `Resolve the target Mitto server (--url/--token/--api-prefix, then
MITTO_URL/MITTO_TOKEN/MITTO_API_PREFIX, then instance.json), confirm it is
reachable, and report which authentication method(s) it has configured.

The token itself is never printed — only its source (flag, env,
instance.json, or none) and an 8-character SHA-256 fingerprint, useful for
confirming two token values match (e.g. before/after "mitto auth rotate")
without ever exposing the secret.`,
	Args: cobra.NoArgs,
	RunE: runAuthStatus,
}

func init() {
	authCmd.AddCommand(authStatusCmd)
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	c, err := newClient(&authFlags)
	if err != nil {
		return err
	}
	// newClient already resolved this successfully; re-resolving is cheap
	// and lets this command report the target fields without newClient
	// exposing its internal *target.
	t, terr := resolveTarget(&authFlags)
	if terr != nil {
		return newExitCodeError(exitUnreachable, terr)
	}

	status := authStatus{
		URL:         t.URL,
		APIPrefix:   t.APIPrefix,
		TokenSource: tokenSource(&authFlags),
	}
	if t.Token != "" {
		status.TokenFingerprint = instancefile.Fingerprint(t.Token)
	}
	if inst, ierr := instancefile.Read(); ierr == nil {
		status.PID = inst.PID
		status.ExternalURL = inst.ExternalURL
	}

	// GET /api/health proves basic reachability. It is intentionally public
	// (docs/devel/web-interface.md), so success here does NOT validate the
	// token — see the authenticated probe below.
	if _, herr := c.GetHealth(); herr != nil {
		return classify(herr)
	}
	status.Reachable = true

	// GET /api/auth-info reports which auth methods are configured; also
	// public, so it needs no token either. A failure here is non-fatal —
	// the fields just stay false.
	if info, aerr := c.GetAuthInfo(); aerr == nil {
		status.AuthEnabled = info.Simple
		status.Cloudflare = info.Cloudflare
	}

	// If auth is enabled, validate the resolved token with one authenticated
	// call — /api/health and /api/auth-info are both public and would
	// report "reachable" even with a wrong or missing token.
	if status.AuthEnabled || status.Cloudflare {
		if _, lerr := c.ListSessions(); lerr != nil {
			return classify(lerr)
		}
	}

	return emit(cmd, &authFlags, status, statusTableFn(&status))
}
