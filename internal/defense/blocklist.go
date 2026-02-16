package defense

import (
	"net"
	"os"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

// BlockEntry represents a blocked IP with metadata.
type BlockEntry struct {
	IP           string    `json:"ip"`
	BlockedAt    time.Time `json:"blocked_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Reason       string    `json:"reason"`
	RequestCount int       `json:"request_count"`
}

// Blocklist manages blocked IPs with expiration and whitelist support.
type Blocklist struct {
	mu      sync.RWMutex
	entries map[string]*BlockEntry
	cidrs   []*net.IPNet // parsed whitelist CIDRs
}

// NewBlocklist creates a new blocklist with the given whitelist CIDRs.
func NewBlocklist(whitelist []string) *Blocklist {
	b := &Blocklist{
		entries: make(map[string]*BlockEntry),
	}

	// Parse whitelist CIDRs
	for _, cidr := range whitelist {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Try parsing as single IP
			ip := net.ParseIP(cidr)
			if ip != nil {
				// Convert single IP to /32 or /128 CIDR
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				ipNet = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
			}
		}
		if ipNet != nil {
			b.cidrs = append(b.cidrs, ipNet)
		}
	}

	return b
}

// isWhitelisted checks if an IP is in the whitelist.
func (b *Blocklist) isWhitelisted(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	for _, cidr := range b.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// Contains checks if an IP is currently blocked (not whitelisted and not expired).
func (b *Blocklist) Contains(ip string) bool {
	// Always allow whitelisted IPs
	if b.isWhitelisted(ip) {
		return false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, exists := b.entries[ip]
	if !exists {
		return false
	}

	// Check if entry has expired
	if time.Now().After(entry.ExpiresAt) {
		return false
	}

	return true
}

// Add blocks an IP with the given entry.
func (b *Blocklist) Add(entry *BlockEntry) {
	// Don't block whitelisted IPs
	if b.isWhitelisted(entry.IP) {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[entry.IP] = entry
}

// Remove unblocks an IP.
func (b *Blocklist) Remove(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, ip)
}

// GetReason returns the block reason for an IP, or empty string if not blocked.
func (b *Blocklist) GetReason(ip string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, exists := b.entries[ip]
	if !exists {
		return ""
	}

	// Check if entry has expired
	if time.Now().After(entry.ExpiresAt) {
		return ""
	}

	return entry.Reason
}

// CleanExpired removes expired entries and returns the count of removed entries.
func (b *Blocklist) CleanExpired() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	removed := 0
	for ip, entry := range b.entries {
		if now.After(entry.ExpiresAt) {
			delete(b.entries, ip)
			removed++
		}
	}
	return removed
}

// Entries returns a copy of all current entries (for persistence).
func (b *Blocklist) Entries() []*BlockEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entries := make([]*BlockEntry, 0, len(b.entries))
	for _, entry := range b.entries {
		entries = append(entries, entry)
	}
	return entries
}

// Count returns the number of blocked IPs.
func (b *Blocklist) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// Load reads blocklist from a JSON file.
func (b *Blocklist) Load(path string) error {
	var entries []*BlockEntry
	if err := fileutil.ReadJSON(path, &entries); err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet - not an error
		}
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Only load non-expired entries
	now := time.Now()
	for _, entry := range entries {
		if now.Before(entry.ExpiresAt) {
			b.entries[entry.IP] = entry
		}
	}
	return nil
}

// Save persists blocklist to a JSON file atomically.
func (b *Blocklist) Save(path string) error {
	entries := b.Entries()
	return fileutil.WriteJSONAtomic(path, entries, 0644)
}
