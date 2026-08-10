package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/inercia/mitto/pkg/api"

	"github.com/inercia/mitto/internal/instancefile"
)

// defaultAPIPrefix is the only API prefix pkg/api's api.New currently
// supports (it is hardcoded there). Kept as a named constant so the
// mismatch check below has one place to update once mitto-rwxq.7 lands.
const defaultAPIPrefix = "/mitto"

// target is the resolved server address and credential a conversation/auth
// subcommand will connect with, per docs/devel/cli-conversation.md §2.
type target struct {
	URL       string
	Token     string
	APIPrefix string
}

// resolveTarget resolves f into a target using, independently per field:
//
//	explicit flag > MITTO_URL/MITTO_TOKEN/MITTO_API_PREFIX env > instance.json > error
//
// instance.json is read at most once, only if at least one field still needs
// it after flags/env. A missing/stale/corrupt instance file only becomes an
// error if some field actually needed it to resolve.
func resolveTarget(f *serverFlags) (*target, error) {
	t := &target{
		URL:       firstNonEmpty(f.URL, os.Getenv("MITTO_URL")),
		Token:     firstNonEmpty(f.Token, os.Getenv("MITTO_TOKEN")),
		APIPrefix: firstNonEmpty(f.APIPrefix, os.Getenv("MITTO_API_PREFIX")),
	}

	if t.URL != "" && t.Token != "" && t.APIPrefix != "" {
		return t, nil
	}

	inst, err := instancefile.Read()
	switch {
	case err == nil:
		// ok, fall through to per-field fill below
	case errors.Is(err, instancefile.ErrStale):
		// A stale instance still carries a usable url/pid for the error
		// message below (never the token), but its fields must not be used
		// to fill in target: the process that wrote it is gone.
		if t.URL == "" || t.Token == "" || t.APIPrefix == "" {
			return nil, fmt.Errorf("mitto server not running (recorded instance at %s, pid %d, is no longer running); start it with `mitto web` or pass --url/--token", inst.URL, inst.PID)
		}
		return t, nil
	case errors.Is(err, instancefile.ErrNotFound):
		if t.URL == "" || t.Token == "" || t.APIPrefix == "" {
			return nil, fmt.Errorf("mitto server not running (no instance file); start it with `mitto web` or pass --url/--token")
		}
		return t, nil
	default: // ErrCorrupt or unexpected
		if t.URL == "" || t.Token == "" || t.APIPrefix == "" {
			return nil, fmt.Errorf("failed to read instance file: %w", err)
		}
		return t, nil
	}

	if t.URL == "" {
		t.URL = inst.URL
	}
	if t.Token == "" {
		t.Token = inst.Token
	}
	if t.APIPrefix == "" {
		t.APIPrefix = inst.APIPrefix
	}
	return t, nil
}

// firstNonEmpty returns the first non-empty string among vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// newClient resolves f into a target and constructs an SDK client from it.
//
// --api-prefix (and MITTO_API_PREFIX / instance.json's api_prefix) is
// accepted and resolved, but pkg/api's api.New hardcodes "/mitto" today
// (mitto-rwxq.7 tracks adding a WithAPIPrefix option). A resolved prefix
// other than the default fails loudly here with a usage error rather than
// silently connecting against the wrong prefix.
// TODO(mitto-rwxq.7): once WithAPIPrefix exists, pass t.APIPrefix through
// instead of rejecting non-default values.
func newClient(f *serverFlags) (*api.Client, error) {
	t, err := resolveTarget(f)
	if err != nil {
		return nil, newExitCodeError(3, err)
	}
	if t.APIPrefix != "" && t.APIPrefix != defaultAPIPrefix {
		return nil, newExitCodeError(2, fmt.Errorf("--api-prefix %q is not yet supported (only %q); see mitto-rwxq.7", t.APIPrefix, defaultAPIPrefix))
	}

	opts := []api.Option{api.WithTimeout(f.Timeout)}
	if t.Token != "" {
		opts = append(opts, api.WithBearerToken(t.Token))
	}
	return api.New(t.URL, opts...), nil
}
