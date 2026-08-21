package beads

import (
	"context"
	"encoding/json"
	"errors"
)

// RemoteInspector is the optional capability needed to infer legacy folder
// database policy without widening Client for every read-only beads consumer.
type RemoteInspector interface {
	HasDoltRemote(ctx context.Context, dir string) (bool, error)
}

// ErrRemoteInspectionUnsupported means a beads client cannot inspect remotes.
var ErrRemoteInspectionUnsupported = errors.New("beads client does not support Dolt remote inspection")

// HasDoltRemote reports whether dir has at least one configured Dolt remote.
// It returns false without invoking bd for a genuinely uninitialized folder.
func (c *cliClient) HasDoltRemote(ctx context.Context, dir string) (bool, error) {
	if !isInitialized(dir) {
		return false, nil
	}
	out, err := c.runJSONRead(ctx, dir, "dolt", "remote", "list", "--json")
	if err != nil {
		return false, err
	}
	var remotes []json.RawMessage
	if err := json.Unmarshal(out, &remotes); err != nil {
		return false, &CmdError{Err: errors.New("bd returned unexpected Dolt remote format")}
	}
	return len(remotes) > 0, nil
}

// DoltRemoteConfigured invokes the optional remote-inspection capability.
func DoltRemoteConfigured(ctx context.Context, client any, dir string) (bool, error) {
	inspector, ok := client.(RemoteInspector)
	if !ok {
		return false, ErrRemoteInspectionUnsupported
	}
	return inspector.HasDoltRemote(ctx, dir)
}

// HasDoltRemote delegates to the wrapped client without caching remote details.
func (c *CachingClient) HasDoltRemote(ctx context.Context, dir string) (bool, error) {
	return DoltRemoteConfigured(ctx, c.inner, dir)
}

var _ RemoteInspector = (*cliClient)(nil)
var _ RemoteInspector = (*CachingClient)(nil)
