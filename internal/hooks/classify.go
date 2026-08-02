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

import "regexp"

// transientHookPattern is the set of compiled regexes that identify
// transient (self-healing) failures in hook output. Adding a new pattern
// here is the intended extension point.
type transientHookPattern struct {
	re     *regexp.Regexp
	reason string
}

// transientHookPatterns is evaluated in order; the first match wins.
//
// Anchored on the strings observed from `cloudflared` when the host's
// systemd-resolved / dnsmasq / 127.0.0.53:53 briefly refuses connections
// while the process is bootstrapping:
//
//	Failed to fetch features ... lookup cfd-features.cloudflare.com on 127.0.0.53:53: read udp ...: connection refused
//	the DNS query failed error=lookup features.argotunnel.com on 127.0.0.53:53: ...
var transientHookPatterns = []transientHookPattern{
	{
		re:     regexp.MustCompile(`lookup [^\s]+ on [^\s]+:53: (read udp|read tcp|dial [^\s]+): connection refused`),
		reason: "loopback DNS refused connection during cloudflared bootstrap",
	},
	{
		re:     regexp.MustCompile(`the DNS query failed error=lookup [^\s]+ on [^\s]+:53:`),
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
