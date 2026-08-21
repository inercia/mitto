package middleware

import (
	"net/http/httptest"
	"testing"
)

// TestIsMobileUserAgent covers the classifier that drives Split-IP damping.
// Positive cases are drawn from real UA strings observed in access.log and
// from the frontend regex (useTheme.js / useWSMobileResilience.js) that this
// helper mirrors. Negative cases are common desktop UAs plus the empty string.
func TestIsMobileUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want bool
	}{
		// Mobile (should damp Split-IP warnings)
		{
			name: "iPhone Safari (bead example)",
			ua:   "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.7 Mobile/15E148 Safari/604.1",
			want: true,
		},
		{"iPad Safari", "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1", true},
		{"iPod touch", "Mozilla/5.0 (iPod touch; CPU iPhone OS 15_0) Safari/604.1", true},
		{"Android Chrome", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36", true},
		{"webOS", "Mozilla/5.0 (webOS/2.2.4; U; en-US) AppleWebKit/534.6 Safari/534.6", true},
		{"BlackBerry", "Mozilla/5.0 (BlackBerry; U; BlackBerry 9900) AppleWebKit/534.11 Safari/534.11", true},
		{"IEMobile", "Mozilla/5.0 (compatible; MSIE 10.0; Windows Phone 8.0; IEMobile/10.0)", true},
		{"Opera Mini", "Opera/9.80 (J2ME/MIDP; Opera Mini/7.1.32444/29.3417; U; en) Presto/2.8.119 Version/11.10", true},
		{"case-insensitive", "SOME/1.0 (IPHONE) MyBrowser/1", true},

		// Desktop (should keep the WARN behavior)
		{"empty UA", "", false},
		{"desktop Chrome on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", false},
		{"desktop Firefox on Linux", "Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0", false},
		{"desktop Safari on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", false},
		{"desktop Edge on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", false},
		{"curl", "curl/8.4.0", false},
		{"generic bot", "Mozilla/5.0 (compatible; SomeBot/1.0)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMobileUserAgent(tt.ua); got != tt.want {
				t.Errorf("isMobileUserAgent(%q) = %v, want %v", tt.ua, got, tt.want)
			}
		})
	}
}

// TestSetSplitIPFlag_NoHolder confirms the setter is a safe no-op when the
// access-log middleware has not injected an AuthAnomaly holder into the
// request context (e.g. access logging disabled). Mirrors the equivalent
// SetAuthIdentity no-op contract.
func TestSetSplitIPFlag_NoHolder(t *testing.T) {
	// Should not panic; nothing to assert beyond that.
	req := httptest.NewRequest("POST", "/api/login", nil)
	SetSplitIPFlag(req)
}
