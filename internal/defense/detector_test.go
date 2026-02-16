package defense

import "testing"

func TestIsSuspiciousPath(t *testing.T) {
	tests := []struct {
		path       string
		suspicious bool
	}{
		{"/.env", true},
		{"/.git/config", true},
		{"/wp-admin", true},
		{"/phpmyadmin", true},
		{"/api/.env", true},
		{"/api/v1/.env", true},
		{"/api/config.json", true},
		{"/admin", true},
		{"/backup", true},

		// Non-suspicious paths
		{"/", false},
		{"/api/sessions", false},
		{"/api/health", false},
		{"/static/app.js", false},
		{"/index.html", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsSuspiciousPath(tt.path)
			if got != tt.suspicious {
				t.Errorf("IsSuspiciousPath(%q) = %v, want %v", tt.path, got, tt.suspicious)
			}
		})
	}
}

func TestIsSuspiciousUserAgent(t *testing.T) {
	tests := []struct {
		ua         string
		suspicious bool
	}{
		{"sqlmap/1.5", true},
		{"Nikto/2.1.6", true},
		{"Mozilla/5.0 (compatible; Nmap Scripting Engine)", true},
		{"zgrab/0.x", true},
		{"gobuster/3.1.0", true},
		{"nuclei - projectdiscovery.io", true},

		// Non-suspicious (legitimate browsers)
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36", false},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", false},
		{"Safari/605.1.15", false},
	}

	for _, tt := range tests {
		t.Run(tt.ua, func(t *testing.T) {
			got := IsSuspiciousUserAgent(tt.ua)
			if got != tt.suspicious {
				t.Errorf("IsSuspiciousUserAgent(%q) = %v, want %v", tt.ua, got, tt.suspicious)
			}
		})
	}
}
