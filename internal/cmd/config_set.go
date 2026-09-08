package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/config/configpath"
	"github.com/inercia/mitto/internal/config/configsvc"
	"github.com/inercia/mitto/pkg/api"
)

// configSetFlags mirrors serverFlags but is registered LOCALLY on
// configSetCmd, for the same reason configGetFlags is (see config_get.go).
var configSetFlags serverFlags

var (
	configSetOffline  bool
	configSetDryRun   bool
	configSetRevision string
)

// configSetRawEntry is one accumulated --set*/--set-string/--set-json/
// --set-file occurrence, still in raw (unparsed) form.
type configSetRawEntry struct {
	mode configpath.Mode
	raw  string
}

// configSetOrder accumulates every --set/--set-string/--set-json/--set-file
// occurrence in true command-line order, regardless of which flag produced
// it. pflag calls Value.Set() once per occurrence, left-to-right across the
// whole argv — including across distinct flags — so a single shared slice
// (rather than each flag keeping its own StringArray) is what lets
// expandConfigSetOrder later reconstruct cross-flag argument-order
// precedence (docs/config/config-cli.md "Mixed modes at the same exact
// path": `--set a=1 --set-json a=2` must resolve to the JSON value, and
// reversing the flags must reverse the winner).
var configSetOrder []configSetRawEntry

// configSetOrderedValue is a pflag.Value bound to one of the four --set*
// flags. Every occurrence appends {mode, raw} onto the shared
// configSetOrder slice above instead of a per-flag list.
type configSetOrderedValue struct {
	mode configpath.Mode
}

func (v *configSetOrderedValue) String() string { return "" }
func (v *configSetOrderedValue) Type() string   { return "stringArray" }
func (v *configSetOrderedValue) Set(raw string) error {
	configSetOrder = append(configSetOrder, configSetRawEntry{mode: v.mode, raw: raw})
	return nil
}

// configSetKeyResult reports one op's outcome, unifying the shapes of
// configsvc.MutateResult (offline) and api.ConfigPatchResult (live) into a
// single output type.
type configSetKeyResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// configSetResponse is the single output shape for both offline and live
// mode.
type configSetResponse struct {
	DryRun   bool                 `json:"dry_run"`
	Applied  []configSetKeyResult `json:"applied"`
	Revision string               `json:"revision,omitempty"`
}

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Write one or more Mitto configuration values",
	Long: `Write one or more configuration values by dotted/indexed path (see
docs/config/config-cli.md for the path grammar and precedence rules).

By default this talks to a running "mitto web" server (discovered via
--url/--token, $MITTO_URL/$MITTO_TOKEN, or instance.json), applying the
write via a validated, batched POST /api/config/patch call. Pass --offline
to write the local settings.json directly instead, with no server, no
network, and no Keychain access.

At least one of --set, --set-string, --set-json, or --set-file is required,
each repeatable and mixable; precedence for the same exact path is purely by
command-line order, not by flag/mode. Ancestor/descendant path conflicts
(e.g. both "a.b" and "a") are rejected.

Examples:
  mitto config set --set web.port=9090
  mitto config set --set 'task_label_colors[0].color=#ef4444'
  mitto config set --set-json 'shortcuts={"icon":"star","prompt":"review"}'
  mitto config set --offline --dry-run --set web.port=9090
  mitto config set --set-file notes=./notes.txt`,
	Args: cobra.NoArgs,
	RunE: runConfigSet,
}

func init() {
	configCmd.AddCommand(configSetCmd)

	configSetCmd.Flags().StringVar(&configSetFlags.URL, "url", "", "Mitto server base URL (default: $MITTO_URL, then instance.json)")
	configSetCmd.Flags().StringVar(&configSetFlags.Token, "token", "", "Bearer token for authentication (default: $MITTO_TOKEN, then instance.json)")
	configSetCmd.Flags().StringVar(&configSetFlags.APIPrefix, "api-prefix", "", "API path prefix (default: $MITTO_API_PREFIX, then instance.json)")
	configSetCmd.Flags().DurationVar(&configSetFlags.Timeout, "timeout", 30*time.Second, "HTTP request timeout")
	configSetCmd.Flags().StringVar(&configSetFlags.Output, "output", "json", "Output format: json, yaml, or table")
	configSetCmd.Flags().BoolVar(&configSetFlags.NoColor, "no-color", false, "Disable colored/styled output (also: $NO_COLOR)")
	configSetCmd.Flags().StringVar(&configSetFlags.Style, "style", "auto", "Styled-mode color palette: auto, dark, or light (also: $GLAMOUR_STYLE)")

	configSetCmd.Flags().Var(&configSetOrderedValue{mode: configpath.ModeTyped}, "set",
		"path=value assignment with auto-typed value (repeatable, comma-separated; see docs/config/config-cli.md)")
	configSetCmd.Flags().Var(&configSetOrderedValue{mode: configpath.ModeString}, "set-string",
		"path=value assignment, value always forced to a string (repeatable, comma-separated)")
	configSetCmd.Flags().Var(&configSetOrderedValue{mode: configpath.ModeJSON}, "set-json",
		"path=value assignment, value must be strict JSON (repeatable, comma-separated)")
	configSetCmd.Flags().Var(&configSetOrderedValue{mode: configpath.ModeFile}, "set-file",
		"path=source assignment; source is a file path or - for stdin (repeatable; at most one stdin source per invocation)")

	configSetCmd.Flags().BoolVar(&configSetOffline, "offline", false, "Write local settings.json directly; no server, no network, no Keychain access")
	configSetCmd.Flags().BoolVar(&configSetDryRun, "dry-run", false, "Validate the batch without persisting anything")
	configSetCmd.Flags().StringVar(&configSetRevision, "revision", "", "Optimistic-concurrency token from a previous snapshot's revision; a stale value fails the whole batch")
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	assignments, err := expandConfigSetOrder()
	if err != nil {
		return err
	}
	if len(assignments) == 0 {
		return newExitCodeError(exitUsage, fmt.Errorf("at least one --set/--set-string/--set-json/--set-file is required"))
	}

	opSet, err := configpath.Resolve(assignments)
	if err != nil {
		return newExitCodeError(exitUsage, err)
	}

	if configSetOffline {
		return runConfigSetOffline(cmd, opSet)
	}
	return runConfigSetLive(cmd, opSet)
}

// expandConfigSetOrder expands every accumulated {mode, raw} entry into
// configpath.Assignments in true command-line order, then resets the shared
// accumulator so a later Execute() call in the same process (tests) starts
// clean. --set-file entries are expanded here — reading the file/stdin
// content is a CLI-layer concern; configpath.ParseSetFile itself never
// touches the filesystem.
func expandConfigSetOrder() ([]configpath.Assignment, error) {
	entries := configSetOrder
	configSetOrder = nil

	var out []configpath.Assignment
	stdinUsed := false
	for _, e := range entries {
		switch e.mode {
		case configpath.ModeTyped:
			as, err := configpath.ParseSet(e.raw)
			if err != nil {
				return nil, newExitCodeError(exitUsage, err)
			}
			out = append(out, as...)
		case configpath.ModeString:
			as, err := configpath.ParseSetString(e.raw)
			if err != nil {
				return nil, newExitCodeError(exitUsage, err)
			}
			out = append(out, as...)
		case configpath.ModeJSON:
			as, err := configpath.ParseSetJSON(e.raw)
			if err != nil {
				return nil, newExitCodeError(exitUsage, err)
			}
			out = append(out, as...)
		case configpath.ModeFile:
			a, err := expandSetFileEntry(e.raw, &stdinUsed)
			if err != nil {
				return nil, err
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// expandSetFileEntry reads the bounded content for one --set-file
// "path=source" entry (source is a file path, or "-" for stdin, capped at
// exactly one stdin source per invocation) and builds the resulting
// Assignment via the pure configpath.ParseSetFile.
func expandSetFileEntry(raw string, stdinUsed *bool) (configpath.Assignment, error) {
	key, source, err := splitSetFileArg(raw)
	if err != nil {
		return configpath.Assignment{}, newExitCodeError(exitUsage, err)
	}

	var content string
	if source == "-" {
		if *stdinUsed {
			return configpath.Assignment{}, newExitCodeError(exitUsage,
				fmt.Errorf(`--set-file: at most one stdin ("-") source is allowed per invocation`))
		}
		*stdinUsed = true
		content, err = readBoundedContent(os.Stdin, configpath.MaxFileValueBytes)
	} else {
		var f *os.File
		f, err = os.Open(source)
		if err != nil {
			return configpath.Assignment{}, newExitCodeError(exitUsage, fmt.Errorf("--set-file %s: %w", key, err))
		}
		defer f.Close()
		content, err = readBoundedContent(f, configpath.MaxFileValueBytes)
	}
	if err != nil {
		return configpath.Assignment{}, newExitCodeError(exitUsage, fmt.Errorf("--set-file %s: %w", key, err))
	}

	a, err := configpath.ParseSetFile(key + "=" + content)
	if err != nil {
		return configpath.Assignment{}, newExitCodeError(exitUsage, err)
	}
	return a, nil
}

// splitSetFileArg splits a --set-file raw value ("path=source") into its
// path and source parts. It mirrors configpath's own (unexported)
// splitAssignment — split on the first unescaped, top-level '=', treating
// [...]/{...} as opaque and "..." as an opaque quoted literal — so a path
// containing an escaped '=' or an index segment splits identically to how
// the pure parser would split it. This logic lives here (not in
// configpath) because reading the source is a CLI-layer concern the pure
// parser deliberately never performs (see ParseSetFile's doc comment).
func splitSetFileArg(raw string) (path, source string, err error) {
	depth := 0
	inQuote := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case inQuote:
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		case c == '=' && depth == 0:
			return raw[:i], raw[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("missing '=' in --set-file assignment")
}

// readBoundedContent reads at most max bytes from r, returning an error
// (never a silent truncation) if the content is larger than max — the
// hard ceiling reserved by configpath.MaxFileValueBytes.
func readBoundedContent(r io.Reader, max int) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return "", err
	}
	if len(data) > max {
		return "", fmt.Errorf("content exceeds max size of %d bytes", max)
	}
	return string(data), nil
}

func runConfigSetOffline(cmd *cobra.Command, opSet configpath.OpSet) error {
	result, err := configsvc.Mutate(configsvc.MutateRequest{
		Set:      opSet,
		Revision: configSetRevision,
		DryRun:   configSetDryRun,
	})
	if err != nil {
		return classifyConfigsvcErr(err)
	}

	resp := configSetResponse{DryRun: configSetDryRun}
	for _, af := range result.AppliedFields {
		resp.Applied = append(resp.Applied, configSetKeyResult{Path: af.Path, Status: offlineFieldStatus(af, configSetDryRun)})
	}
	if !configSetDryRun {
		if snap, serr := configsvc.ReadSnapshot(); serr == nil {
			resp.Revision = snap.Revision
		}
	}
	return emitConfigSetResponse(cmd, &resp)
}

// offlineFieldStatus mirrors internal/web/handlers/config_patch.go's
// applyPatchedField status classification for the offline write path: there
// is no running server process here to refresh an in-memory mirror for a
// LivenessLive field, so "applied" (the field took effect purely by being
// persisted) covers both LivenessLive and LivenessUnspecified — only
// LivenessRequiresRestart needs a distinct status.
func offlineFieldStatus(af configsvc.AppliedField, dryRun bool) string {
	if dryRun {
		return "would_apply"
	}
	if af.Liveness == configsvc.LivenessRequiresRestart {
		return "restart_required"
	}
	return "applied"
}

// classifyConfigsvcErr maps a configsvc.Error (offline write) to the
// exit-code contract in docs/config/config-cli.md: 2 (usage/validation), 5
// (not found), 1 (generic — concurrency/lock/IO failures the caller can
// only retry, not fix by editing the command).
func classifyConfigsvcErr(err error) error {
	var cerr *configsvc.Error
	if !errors.As(err, &cerr) {
		return newExitCodeError(exitGeneric, err)
	}
	switch cerr.Kind {
	case configsvc.ErrKindUnknownField, configsvc.ErrKindValidation, configsvc.ErrKindConflict,
		configsvc.ErrKindStructure, configsvc.ErrKindRejected, configsvc.ErrKindReadOnly:
		return newExitCodeError(exitUsage, err)
	case configsvc.ErrKindNotFound:
		return newExitCodeError(exitNotFound, err)
	default: // ErrKindRevisionMismatch, ErrKindLocked, ErrKindIO
		return newExitCodeError(exitGeneric, err)
	}
}

func runConfigSetLive(cmd *cobra.Command, opSet configpath.OpSet) error {
	c, err := newConfigGetClient(&configSetFlags)
	if err != nil {
		return err
	}

	ops := make([]api.ConfigPatchOp, 0, len(opSet.Ops))
	for _, op := range opSet.Ops {
		val, jerr := op.Value.ToJSON()
		if jerr != nil {
			return newExitCodeError(exitUsage, fmt.Errorf("%s: %w", op.Path.String(), jerr))
		}
		ops = append(ops, api.ConfigPatchOp{Path: op.Path.String(), Value: val})
	}

	ctx, cancel := context.WithTimeout(context.Background(), configSetFlags.Timeout)
	defer cancel()

	result, err := c.ConfigPatch(ctx, api.ConfigPatchRequest{
		Ops:      ops,
		Revision: configSetRevision,
		DryRun:   configSetDryRun,
	})
	if err != nil {
		return classifyConfigSetErr(err)
	}

	resp := configSetResponse{DryRun: result.DryRun, Revision: result.Revision}
	for _, a := range result.Applied {
		resp.Applied = append(resp.Applied, configSetKeyResult{Path: a.Path, Status: a.Status})
	}
	return emitConfigSetResponse(cmd, &resp)
}

// classifyConfigSetErr maps a live ConfigPatch failure to the same 2/5/1
// outcomes as classifyConfigsvcErr's offline mapping (mitto-4rz.5 Plan,
// decision 6). This deliberately diverges from classify()'s general-purpose
// 403->exitAuthFailure mapping (used by every other conversation/auth
// command, where a 403 genuinely means "you are not allowed to do this"):
// a 403 from POST /api/config/patch always means "this field is
// rejected/read-only" (writeConfigsvcError maps ErrKindRejected/
// ErrKindReadOnly to 403), since the request already passed the
// instance-bearer auth gate — so it is a usage error here, not an auth
// failure. A 401 (only ever issued by the instance-bearer gate itself, not
// by this endpoint's own logic) still falls through to classify()'s
// exitAuthFailure, as does anything else not specific to this endpoint
// (unreachable, generic).
func classifyConfigSetErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, api.ErrForbidden) || errors.Is(err, api.ErrBadRequest) {
		return newExitCodeError(exitUsage, err)
	}
	if errors.Is(err, api.ErrConflict) {
		return newExitCodeError(exitGeneric, err)
	}
	return classify(err)
}

func emitConfigSetResponse(cmd *cobra.Command, resp *configSetResponse) error {
	return emit(cmd, &configSetFlags, resp, configSetTableFn(resp))
}

func configSetTableFn(resp *configSetResponse) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		rows := make([][]string, 0, len(resp.Applied))
		for _, a := range resp.Applied {
			rows = append(rows, []string{a.Path, a.Status})
		}
		return []string{"PATH", "STATUS"}, rows
	}
}
