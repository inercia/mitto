package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/config/configpath"
	"github.com/inercia/mitto/internal/config/configsvc"
	"github.com/inercia/mitto/pkg/api"
)

// configGetFlags mirrors serverFlags but is registered LOCALLY on
// configGetCmd (not persistent on configCmd): configCmd's other child,
// config create, already owns -o/--output for a *directory*, so a
// persistent --output (format) flag on the parent would collide with it
// (mitto-4rz.4 Plan, decision 1).
var configGetFlags serverFlags

var (
	configGetOffline   bool
	configGetEffective bool
	configGetExplain   bool
	configGetRaw       bool
)

// configFieldView is the --explain rendering of one resolved path: value
// plus provenance/redaction metadata. Offline-only — see the --offline
// gate in runConfigGet (the live /api/config/snapshot resource has no
// per-path provenance to report).
type configFieldView struct {
	Path       string      `json:"path"`
	Provenance string      `json:"provenance"`
	Redacted   bool        `json:"redacted"`
	Value      interface{} `json:"value"`
}

var configGetCmd = &cobra.Command{
	Use:   "get [PATH]",
	Short: "Read a Mitto configuration value",
	Long: `Read a configuration value by dotted/indexed path (see
docs/config/config-cli.md for the path grammar), or the whole redacted
config when PATH is omitted.

By default this talks to a running "mitto web" server (discovered via
--url/--token, $MITTO_URL/$MITTO_TOKEN, or instance.json), returning its
stored settings.json — never a delete/mutate, and secrets are always
redacted. Pass --offline to read the local settings.json directly instead,
with no server, no network, and no Keychain access; --offline also unlocks
--effective (show a compile-time default when nothing is stored) and
--explain (show provenance/redaction alongside the value) — the live
snapshot has no per-path provenance to report.

Examples:
  mitto config get web.port
  mitto config get --offline --effective mcp.port
  mitto config get --offline --explain 'task_label_colors[0].color'
  mitto config get --raw web.port          # bare scalar, for scripting
  mitto config get --output yaml           # whole config as YAML`,
	Args: cobra.MaximumNArgs(1),
	RunE: runConfigGet,
}

func init() {
	configCmd.AddCommand(configGetCmd)

	configGetCmd.Flags().StringVar(&configGetFlags.URL, "url", "", "Mitto server base URL (default: $MITTO_URL, then instance.json)")
	configGetCmd.Flags().StringVar(&configGetFlags.Token, "token", "", "Bearer token for authentication (default: $MITTO_TOKEN, then instance.json)")
	configGetCmd.Flags().StringVar(&configGetFlags.APIPrefix, "api-prefix", "", "API path prefix (default: $MITTO_API_PREFIX, then instance.json)")
	configGetCmd.Flags().DurationVar(&configGetFlags.Timeout, "timeout", 30*time.Second, "HTTP request timeout")
	configGetCmd.Flags().StringVar(&configGetFlags.Output, "output", "json", "Output format: json, yaml, or table (see --raw for a bare scalar)")
	configGetCmd.Flags().BoolVar(&configGetFlags.NoColor, "no-color", false, "Disable colored/styled output (also: $NO_COLOR)")
	configGetCmd.Flags().StringVar(&configGetFlags.Style, "style", "auto", "Styled-mode color palette: auto, dark, or light (also: $GLAMOUR_STYLE)")

	configGetCmd.Flags().BoolVar(&configGetOffline, "offline", false, "Read local settings.json directly; no server, no network, no Keychain access")
	configGetCmd.Flags().BoolVar(&configGetEffective, "effective", false, "Show the compile-time effective default when nothing is stored (requires --offline)")
	configGetCmd.Flags().BoolVar(&configGetExplain, "explain", false, "Show provenance and redaction status alongside the value (requires --offline and PATH)")
	configGetCmd.Flags().BoolVar(&configGetRaw, "raw", false, "Print a bare scalar with no JSON/YAML quoting; errors if the value is an object or array")
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	if configGetRaw && configGetExplain {
		return newExitCodeError(exitUsage, fmt.Errorf("--raw and --explain are mutually exclusive"))
	}
	if configGetExplain && len(args) == 0 {
		return newExitCodeError(exitUsage, fmt.Errorf("--explain requires a PATH"))
	}
	if (configGetEffective || configGetExplain) && !configGetOffline {
		return newExitCodeError(exitUsage, fmt.Errorf("--effective and --explain require --offline: the live snapshot has no per-path provenance"))
	}

	var path configpath.Path
	var pathStr string
	if len(args) == 1 {
		pathStr = args[0]
		p, err := configpath.ParsePath(pathStr)
		if err != nil {
			return newExitCodeError(exitUsage, fmt.Errorf("invalid path %q: %w", pathStr, err))
		}
		path = p
	}

	if configGetOffline {
		return runConfigGetOffline(cmd, path, pathStr)
	}
	return runConfigGetLive(cmd, path, pathStr)
}

func runConfigGetOffline(cmd *cobra.Command, path configpath.Path, pathStr string) error {
	snap, err := configsvc.ReadSnapshot()
	if err != nil {
		return newExitCodeError(exitGeneric, err)
	}

	if len(path) == 0 {
		whole, ok := snap.GetWhole()
		if !ok {
			return newExitCodeError(exitNotFound, fmt.Errorf("settings file does not exist"))
		}
		return emitResolvedValue(cmd, "", whole, string(configsvc.ProvenanceStored), false)
	}

	fv, ferr := snap.Get(path)
	if ferr != nil {
		return newExitCodeError(exitGeneric, ferr)
	}
	if fv.Provenance == configsvc.ProvenanceUnset {
		return newExitCodeError(exitNotFound, fmt.Errorf("path %q not found (no stored value and no known default)", pathStr))
	}
	if fv.Provenance == configsvc.ProvenanceEffective && !configGetEffective {
		return newExitCodeError(exitNotFound, fmt.Errorf("path %q not stored (pass --effective to see its compile-time default)", pathStr))
	}
	return emitResolvedValue(cmd, pathStr, fv.Value, string(fv.Provenance), fv.Redacted)
}

func runConfigGetLive(cmd *cobra.Command, path configpath.Path, pathStr string) error {
	c, err := newConfigGetClient(&configGetFlags)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), configGetFlags.Timeout)
	defer cancel()

	snap, err := c.ConfigSnapshot(ctx)
	if err != nil {
		return classify(err)
	}
	if !snap.Exists {
		return newExitCodeError(exitNotFound, fmt.Errorf("settings file does not exist on server"))
	}

	if len(path) == 0 {
		return emitResolvedValue(cmd, "", snap.Config, "", false)
	}

	val, ok := navigatePath(snap.Config, path)
	if !ok {
		return newExitCodeError(exitNotFound, fmt.Errorf("path %q not found", pathStr))
	}
	return emitResolvedValue(cmd, pathStr, val, "", false)
}

// newConfigGetClient builds the SDK client for live mode, refusing to pair
// an explicitly-given remote target with an implicitly-adopted local
// instance.json bearer token: resolveTarget resolves URL/token/prefix
// independently, so an explicit --url without an explicit --token would
// otherwise silently attach this machine's local server credential to
// whatever host --url points at (mitto-4rz.4 Plan, decision 7).
func newConfigGetClient(f *serverFlags) (*api.Client, error) {
	explicitURL := firstNonEmpty(f.URL, os.Getenv("MITTO_URL"))
	explicitToken := firstNonEmpty(f.Token, os.Getenv("MITTO_TOKEN"))
	if explicitURL != "" && explicitToken == "" {
		return nil, newExitCodeError(exitUsage, fmt.Errorf(
			"--url (or $MITTO_URL) was given without --token (or $MITTO_TOKEN): refusing to attach the local instance.json bearer token to an explicit remote target; pass --token explicitly"))
	}
	return newClient(f)
}

// navigatePath walks root (decoded JSON: map[string]interface{} /
// []interface{} / scalars) following path. ok is false only when a key is
// absent or an index is out of range — a JSON null value at the target key
// still returns ok=true with a nil value (missing vs. stored-null).
func navigatePath(root interface{}, path configpath.Path) (interface{}, bool) {
	cur := root
	for _, seg := range path {
		if seg.IsIndex {
			arr, ok := cur.([]interface{})
			if !ok || seg.Index < 0 || seg.Index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.Index]
			continue
		}
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, present := m[seg.Key]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// emitResolvedValue is the single output path for both live and offline
// modes once a value has been resolved: --raw wins (bare scalar, rejects
// objects/arrays), then --explain (structured provenance view, offline
// only — callers pass provenance="" from live mode since it's unreachable
// there), then the plain value via the shared emit() helper.
func emitResolvedValue(cmd *cobra.Command, pathStr string, val interface{}, provenance string, redacted bool) error {
	if configGetRaw {
		if err := renderRawScalar(cmd.OutOrStdout(), val); err != nil {
			return newExitCodeError(exitUsage, err)
		}
		return nil
	}
	if configGetExplain {
		fv := &configFieldView{Path: pathStr, Provenance: provenance, Redacted: redacted, Value: val}
		return emit(cmd, &configGetFlags, fv, fieldViewTableFn(fv))
	}
	label := pathStr
	if label == "" {
		label = "(whole config)"
	}
	return emit(cmd, &configGetFlags, val, genericValueTableFn(label, val))
}

// renderRawScalar prints v with no JSON/YAML quoting, for shell scripting.
// It rejects objects/arrays (exit 2) rather than dumping their Go/JSON
// representation, per the CLI contract's scalar-mode requirement.
func renderRawScalar(w interface{ Write([]byte) (int, error) }, v interface{}) error {
	switch t := v.(type) {
	case nil:
		_, err := fmt.Fprintln(w, "null")
		return err
	case string:
		_, err := fmt.Fprintln(w, t)
		return err
	case bool, float64, json.Number, int64, int:
		_, err := fmt.Fprintln(w, t)
		return err
	default:
		return fmt.Errorf("--raw requires a scalar value; got %T", v)
	}
}

func fieldViewTableFn(fv *configFieldView) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		rows := [][]string{
			{"PATH", fv.Path},
			{"PROVENANCE", fv.Provenance},
			{"REDACTED", fmt.Sprintf("%t", fv.Redacted)},
			{"VALUE", fmt.Sprintf("%v", fv.Value)},
		}
		return []string{"FIELD", "VALUE"}, rows
	}
}

// genericValueTableFn renders an arbitrary (possibly nested) resolved
// value as a single-row table. Table output is explicitly unstable for
// this command (nested values render as compact JSON); scripts should use
// --output json/yaml or --raw instead.
func genericValueTableFn(label string, v interface{}) func() ([]string, [][]string) {
	return func() ([]string, [][]string) {
		b, err := json.Marshal(v)
		val := string(b)
		if err != nil {
			val = fmt.Sprintf("%v", v)
		}
		return []string{"PATH", "VALUE"}, [][]string{{label, val}}
	}
}
