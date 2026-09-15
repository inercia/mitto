// Package hooks — transient-error classifier for hook stdout/stderr output.
//
// Some hook commands (notably `cloudflared tunnel run`) can emit noisy but
// benign bootstrap failures when the host's loopback DNS resolver briefly
// refuses connections during startup. Those look identical to fatal errors
// in the log stream, causing loud red-toast events on the frontend even
// though the hook self-heals seconds later.
//
// ClassifyHookOutput inspects a captured stdout+stderr buffer and returns
// (transient=true, reason) if the output matches a known transient pattern,
// so callers can downgrade the log level and rate-limit UI notifications.
package hooks

import (
	"regexp"
	"strings"
)

// transientHookPattern is the set of compiled regexes that identify
// transient (self-healing) failures in hook output. Adding a new pattern
// here is the intended extension point.
type transientHookPattern struct {
	re     *regexp.Regexp
	reason string
}

// transientHookPatterns is evaluated in order; the first match wins.
//
// Anchored on the strings observed from `cloudflared` when a DNS resolver —
// whether the host's loopback systemd-resolved/dnsmasq (127.0.0.53:53) or a
// corporate resolver (e.g. 10.102.2.247:53) — briefly fails to answer
// cloudflared's tunnel-edge lookups while the process is bootstrapping:
//
//	Failed to fetch features ... lookup cfd-features.cloudflare.com on 127.0.0.53:53: read udp ...: connection refused
//	Failed to fetch features ... lookup cfd-features.argotunnel.com on 10.102.2.247:53: dial udp ...: i/o timeout
//	edge discovery: error looking up Cloudflare edge IPs: the DNS query failed error="lookup _v2-origintunneld._tcp.argotunnel.com on 10.102.2.247:53: dial udp ...: i/o timeout"
//	the DNS query failed error=lookup features.argotunnel.com on 127.0.0.53:53: ...
//
// Note: Go's regexp (RE2) does not support Perl shorthand like \s / \S, so
// we use explicit character classes ([^ ] rather than [^\s]) plus [^\n]* to
// bridge the address-and-port noise between the DNS-lookup preamble and the
// "connection refused"/"i/o timeout" suffix.
var transientHookPatterns = []transientHookPattern{
	{
		re:     regexp.MustCompile(`lookup [^ ]+ on [^ ]+:53: (read udp|read tcp|dial [^ ]+)[^\n]*(connection refused|i/o timeout)`),
		reason: "DNS resolver refused connection or timed out during cloudflared bootstrap",
	},
	{
		re:     regexp.MustCompile(`the DNS query failed error=lookup [^ ]+ on [^ ]+:53:`),
		reason: "transient DNS query failure during cloudflared bootstrap",
	},
}

// ClassifyHookOutput inspects captured hook output and reports whether the
// failure looks transient (self-healing) rather than a genuine hard error.
// Fail-closed: if nothing matches, returns (false, "") so callers keep the
// existing ERROR log level and full broadcast behavior.
func ClassifyHookOutput(output string) (transient bool, reason string) {
	if output == "" {
		return false, ""
	}
	for _, p := range transientHookPatterns {
		if p.re.MatchString(output) {
			return true, p.reason
		}
	}
	return false, ""
}

// pkillSimpleCommandRe matches a *simple* pkill invocation (the command,
// after trimming whitespace, starts with "pkill"). It deliberately does not
// anchor the rest of the line so `pkill -f 'some pattern'` still matches.
var pkillSimpleCommandRe = regexp.MustCompile(`^pkill\b`)

// shellCompoundRe flags characters that indicate the command is more than a
// single simple invocation (command separators, pipes, substitution). Used
// to fail closed on compound shell pipelines that merely happen to contain
// "pkill" somewhere — only a standalone pkill invocation has exit-code
// semantics we can trust.
var shellCompoundRe = regexp.MustCompile("[;&|`]|\\$\\(")

// ClassifyBenignExit inspects a hook command's shape, exit code, and captured
// output to detect known benign no-op failures — cases where a non-zero exit
// carries no diagnostic value and should not be logged as an ERROR or trigger
// a failure notification.
//
// Currently recognizes:
//   - A simple `pkill <args>` invocation exiting with code 1 and no captured
//     output: pkill's exit code 1 means "no processes matched", which is the
//     expected (and common) outcome when a down-hook's cleanup command runs
//     after the target process has already exited on its own.
//
// Fail-closed: returns (false, "") for anything else — compound shell
// pipelines, non-empty output, or other exit codes — so callers keep the
// existing ERROR-level logging and failure-notification behavior.
func ClassifyBenignExit(command string, exitCode int, output string) (benign bool, reason string) {
	trimmed := strings.TrimSpace(command)
	if exitCode == 1 && output == "" && pkillSimpleCommandRe.MatchString(trimmed) && !shellCompoundRe.MatchString(trimmed) {
		return true, "pkill matched no processes"
	}
	return false, ""
}
