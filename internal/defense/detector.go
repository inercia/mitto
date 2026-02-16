package defense

import (
	"strings"
)

// SuspiciousPaths contains paths that indicate automated scanning.
// These are commonly probed by vulnerability scanners and bots.
var SuspiciousPaths = []string{
	"/.env",
	"/.git/",
	"/.git/config",
	"/.aws/",
	"/.htpasswd",
	"/.htaccess",
	"/wp-admin",
	"/wp-login",
	"/wp-content",
	"/phpmyadmin",
	"/phpinfo",
	"/admin",
	"/config.json",
	"/secrets.json",
	"/backup",
	"/dump",
	"/api/.env",
	"/api/v1/.env",
	"/api/v2/.env",
	"/api/.git",
	"/api/config.json",
	"/api/secrets",
	"/.DS_Store",
	"/composer.json",
	"/package.json",
	"/yarn.lock",
	"/Gemfile",
	"/web.config",
	"/server-status",
	"/cgi-bin/",
	"/shell",
	"/cmd",
	"/eval",
	"/exec",
}

// suspiciousUserAgents contains known scanner/bot user agent patterns.
// Note: curl/, python-requests, and go-http-client are intentionally included
// because Mitto has its own dedicated client - any requests from generic HTTP
// clients to external endpoints are unauthorized and likely malicious probes.
var suspiciousUserAgents = []string{
	"sqlmap",
	"nikto",
	"nmap",
	"masscan",
	"zgrab",
	"gobuster",
	"dirbuster",
	"wfuzz",
	"ffuf",
	"nuclei",
	"httpx",
	"curl/",
	"python-requests",
	"go-http-client",
	"scanner",
	"exploit",
	"attack",
}

// IsSuspiciousPath checks if a path matches known scanner patterns.
// Uses HasPrefix only to avoid false positives (e.g., /admin-dashboard should not match /admin).
func IsSuspiciousPath(path string) bool {
	lowerPath := strings.ToLower(path)
	for _, suspicious := range SuspiciousPaths {
		if strings.HasPrefix(lowerPath, suspicious) {
			return true
		}
	}
	return false
}

// IsSuspiciousUserAgent checks if a user agent matches known scanner patterns.
func IsSuspiciousUserAgent(ua string) bool {
	lowerUA := strings.ToLower(ua)
	for _, suspicious := range suspiciousUserAgents {
		if strings.Contains(lowerUA, suspicious) {
			return true
		}
	}
	return false
}
