package acp

import (
	"reflect"
	"testing"
)

// Reproduction tests for mitto-m8nx (AC2): when the agent's MCP-init-timeout
// stderr output carries per-server status lines (e.g. "⏳ yahoo-finance (timed
// out)"), Mitto must be able to recover which MCP server(s) timed out so
// downstream UI notifications can name the offending server instead of only
// the workspace.
//
// The reproduction chunk below is copied verbatim from a production log
// (~/Library/Logs/Mitto/mitto-2026-08-07T*.log, captured during the mitto-m8nx
// investigation):
//
//	output="   ⏳ yahoo-finance (timed out)\n⚠️ MCP initialization timed out after 178s\n"
//
// Investigation findings (see bd comment "Investigation [tier: Reasoning]" on
// mitto-m8nx): grepping the whole tree for "⏳" / "(timed out)" / "(failed)"
// across *.go/*.js/*.json finds no per-server status parsing anywhere.
// MCPInitTimeoutPattern (errors.go) only matches the generic tail line and has
// no capture group, so nothing today can recover "yahoo-finance" from this
// chunk. ExtractMCPTimedOutServers does not exist yet — these tests fail to
// build until the fix phase adds it, which is the expected "red" state for
// this reproduction.

// TestExtractMCPTimedOutServers_ParsesRealAgentChunk reproduces the exact
// single-server chunk observed in production.
func TestExtractMCPTimedOutServers_ParsesRealAgentChunk(t *testing.T) {
	chunk := "   ⏳ yahoo-finance (timed out)\n⚠️ MCP initialization timed out after 178s\n"

	got := ExtractMCPTimedOutServers(chunk)
	want := []string{"yahoo-finance"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractMCPTimedOutServers(%q) = %v, want %v", chunk, got, want)
	}
}

// TestExtractMCPTimedOutServers_MultipleServers verifies multiple per-server
// timeout lines in the same chunk are all recovered, in order, alongside an
// unrelated successful "✅" status line that must be ignored.
func TestExtractMCPTimedOutServers_MultipleServers(t *testing.T) {
	chunk := "   ✅ mitto\n   ⏳ yahoo-finance (timed out)\n   ⏳ some-other-server (timed out)\n⚠️ MCP initialization timed out after 178s\n"

	got := ExtractMCPTimedOutServers(chunk)
	want := []string{"yahoo-finance", "some-other-server"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractMCPTimedOutServers(%q) = %v, want %v", chunk, got, want)
	}
}

// TestExtractMCPTimedOutServers_NoPerServerLine_ReturnsEmpty guards the
// degrade-gracefully requirement: when the generic timeout line arrives
// without any per-server status (e.g. a stderr read-buffer boundary split the
// chunk, or an agent that never emits per-server lines), extraction must
// return an empty slice rather than panic or error, so callers can fall back
// to today's generic notification copy.
func TestExtractMCPTimedOutServers_NoPerServerLine_ReturnsEmpty(t *testing.T) {
	chunk := "MCP initialization timed out after 225s\n"

	got := ExtractMCPTimedOutServers(chunk)
	if len(got) != 0 {
		t.Fatalf("ExtractMCPTimedOutServers(%q) = %v, want empty", chunk, got)
	}
}
