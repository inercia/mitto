package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/inercia/mitto/pkg/api"

	"github.com/inercia/mitto/internal/instancefile"
)

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
// honored via api.WithAPIPrefix (mitto-rwxq.7). When resolveTarget could not
// resolve a prefix from any source (t.APIPrefix == ""), WithAPIPrefix is
// simply not passed, so api.New's zero-config "/mitto" default applies —
// unchanged from prior behavior.
func newClient(f *serverFlags) (*api.Client, error) {
	t, err := resolveTarget(f)
	if err != nil {
		return nil, newExitCodeError(3, err)
	}

	opts := []api.Option{api.WithTimeout(f.Timeout)}
	if t.Token != "" {
		opts = append(opts, api.WithBearerToken(t.Token))
	}
	if t.APIPrefix != "" {
		opts = append(opts, api.WithAPIPrefix(t.APIPrefix))
	}
	return api.New(t.URL, opts...), nil
}
