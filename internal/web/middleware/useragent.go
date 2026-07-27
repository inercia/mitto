package middleware

import "regexp"

// mobileUserAgentRE matches common mobile browser User-Agent strings.
// The pattern intentionally mirrors the frontend regex used in
// web/static/hooks/useTheme.js, useWSMobileResilience.js, and
// components/beads/detail/usePanelChrome.js so that mobile classification
// stays symmetric across the stack. Case-insensitive via (?i).
var mobileUserAgentRE = regexp.MustCompile(`(?i)iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini`)

// isMobileUserAgent reports whether ua looks like a mobile browser. Empty or
// unrecognized user agents return false (i.e. desktop treatment) so existing
// behavior is preserved for anything we do not explicitly recognize.
func isMobileUserAgent(ua string) bool {
	if ua == "" {
		return false
	}
	return mobileUserAgentRE.MatchString(ua)
}
