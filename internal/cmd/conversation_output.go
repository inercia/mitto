package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// outputFormat is a validated --output value (docs/devel/cli-conversation.md §4).
type outputFormat string

const (
	outputTable outputFormat = "table"
	outputJSON  outputFormat = "json"
	outputYAML  outputFormat = "yaml"
)

// parseOutputFormat validates raw against the supported --output values.
// An invalid value is a usage error (exit 2).
func parseOutputFormat(raw string) (outputFormat, error) {
	switch outputFormat(raw) {
	case outputTable, outputJSON, outputYAML:
		return outputFormat(raw), nil
	default:
		return "", newExitCodeError(exitUsage, fmt.Errorf("invalid --output %q: must be table, json, or yaml", raw))
	}
}

// renderJSON writes v to w as indented JSON followed by a trailing newline.
func renderJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderYAML writes v to w as YAML, round-tripped through JSON first so the
// JSON struct tags (not Go field names) drive the emitted key names — the
// documented single source of truth for field names (DDR §4).
func renderYAML(w io.Writer, v any) error {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal to json: %w", err)
	}
	var generic any
	if err := json.Unmarshal(jsonBytes, &generic); err != nil {
		return fmt.Errorf("unmarshal for yaml re-encode: %w", err)
	}
	enc := yaml.NewEncoder(w)
	defer enc.Close()
	return enc.Encode(generic)
}

// renderTable writes a simple tab-aligned table with a header row and a
// "----" separator row, via text/tabwriter (no new dependency, matching the
// existing style in internal/cmd/agents.go). Table output is explicitly
// unstable (DDR §6) and must not be parsed by scripts.
func renderTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, tabJoin(headers))
	seps := make([]string, len(headers))
	for i, h := range headers {
		sep := ""
		for range h {
			sep += "-"
		}
		if sep == "" {
			sep = "-"
		}
		seps[i] = sep
	}
	fmt.Fprintln(tw, tabJoin(seps))
	for _, row := range rows {
		fmt.Fprintln(tw, tabJoin(row))
	}
	return tw.Flush()
}

func tabJoin(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += "\t"
		}
		out += f
	}
	return out
}

// emit dispatches v to the format requested by f.Output: machine output
// (table/json/yaml) always goes to cmd.OutOrStdout() and nothing else does;
// all human chatter and errors belong on stderr instead (DDR §4). tableFn
// renders v as a table (headers + rows) for the default format.
func emit(cmd *cobra.Command, f *serverFlags, v any, tableFn func() ([]string, [][]string)) error {
	format, err := parseOutputFormat(f.Output)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format {
	case outputJSON:
		return renderJSON(out, normalizeForEmptyCollections(v))
	case outputYAML:
		return renderYAML(out, normalizeForEmptyCollections(v))
	default: // outputTable
		headers, rows := tableFn()
		return renderTable(out, headers, rows)
	}
}

// normalizeForEmptyCollections ensures a nil slice passed as v marshals as
// `[]` rather than `null` (DDR §4: a zero-result list command prints `[]`).
// Non-slice values pass through unchanged.
//
// A plain `switch v.(type) { case nil: ... }` only catches the untyped nil
// interface case; a concrete-typed nil slice (e.g. a `var s []Foo` returned
// by a zero-result list call, or `[]Foo(nil)`) is a *non-nil* interface
// value once boxed into `any`, so that case would never fire for the exact
// scenario this function exists to handle. reflect.Value.IsNil() sees
// through the boxing to the underlying nil slice.
func normalizeForEmptyCollections(v any) any {
	if v == nil {
		return []any{}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return []any{}
	}
	return v
}
