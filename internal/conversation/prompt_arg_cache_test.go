package conversation

import (
	"sync"
	"testing"
	"time"
)

// TestPromptArgCache_SetAndGet verifies that a stored value is retrievable.
func TestPromptArgCache_SetAndGet(t *testing.T) {
	c := newPromptArgCache()
	c.Set("myPrompt", "channel", "#general", 0)
	val, ok := c.Get("myPrompt", "channel")
	if !ok {
		t.Fatal("Get returned false, want true")
	}
	if val != "#general" {
		t.Errorf("Get = %q, want %q", val, "#general")
	}
}

// TestPromptArgCache_GetMiss verifies that a missing key returns ("", false).
func TestPromptArgCache_GetMiss(t *testing.T) {
	c := newPromptArgCache()
	val, ok := c.Get("noPrompt", "noParam")
	if ok {
		t.Errorf("Get returned true for missing key, val=%q", val)
	}
	if val != "" {
		t.Errorf("Get value = %q, want empty string", val)
	}
}

// TestPromptArgCache_Expiry verifies that an entry with a short TTL expires.
func TestPromptArgCache_Expiry(t *testing.T) {
	c := newPromptArgCache()
	c.Set("p", "x", "v", 20*time.Millisecond)
	// Should hit immediately.
	if _, ok := c.Get("p", "x"); !ok {
		t.Fatal("Get returned false before expiry")
	}
	time.Sleep(40 * time.Millisecond)
	// Should miss after expiry.
	val, ok := c.Get("p", "x")
	if ok {
		t.Errorf("Get returned true after expiry, val=%q", val)
	}
}

// TestPromptArgCache_NoExpiry verifies that a zero/negative TTL entry never expires.
func TestPromptArgCache_NoExpiry(t *testing.T) {
	c := newPromptArgCache()
	c.Set("p", "y", "forever", 0)
	time.Sleep(20 * time.Millisecond)
	val, ok := c.Get("p", "y")
	if !ok {
		t.Fatal("Get returned false for no-expiry entry after sleep")
	}
	if val != "forever" {
		t.Errorf("Get = %q, want %q", val, "forever")
	}
}

// TestPromptArgCache_FreshNames verifies sorted, per-prompt isolation, and expiry removal.
func TestPromptArgCache_FreshNames(t *testing.T) {
	c := newPromptArgCache()

	// Populate two prompts.
	c.Set("alpha", "zzz", "1", 0)
	c.Set("alpha", "aaa", "2", 0)
	c.Set("alpha", "mmm", "3", 20*time.Millisecond) // will expire
	c.Set("beta", "foo", "4", 0)

	// Before expiry: alpha should have all three names, sorted.
	names := c.FreshNames("alpha")
	if len(names) != 3 {
		t.Fatalf("FreshNames(alpha) = %v, want 3 names", names)
	}
	if names[0] != "aaa" || names[1] != "mmm" || names[2] != "zzz" {
		t.Errorf("FreshNames(alpha) = %v, want [aaa mmm zzz]", names)
	}

	// beta must not bleed into alpha.
	betaNames := c.FreshNames("beta")
	if len(betaNames) != 1 || betaNames[0] != "foo" {
		t.Errorf("FreshNames(beta) = %v, want [foo]", betaNames)
	}

	// After expiry of mmm.
	time.Sleep(40 * time.Millisecond)
	names = c.FreshNames("alpha")
	if len(names) != 2 {
		t.Fatalf("FreshNames(alpha) after expiry = %v, want 2 names", names)
	}
	if names[0] != "aaa" || names[1] != "zzz" {
		t.Errorf("FreshNames(alpha) after expiry = %v, want [aaa zzz]", names)
	}
}

// TestPromptArgCache_FreshNames_EmptyPrompt verifies that an unknown prompt returns nil/empty.
func TestPromptArgCache_FreshNames_EmptyPrompt(t *testing.T) {
	c := newPromptArgCache()
	c.Set("other", "p", "v", 0)
	names := c.FreshNames("noSuchPrompt")
	if len(names) != 0 {
		t.Errorf("FreshNames for unknown prompt = %v, want empty", names)
	}
}

// TestPromptArgCache_Race exercises Set/Get/FreshNames under the race detector.
func TestPromptArgCache_Race(t *testing.T) {
	c := newPromptArgCache()
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Set("prompt", "param", "value", 5*time.Millisecond)
				c.Get("prompt", "param")
				c.FreshNames("prompt")
			}
		}(i)
	}
	wg.Wait()
}
