package bdexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sync"
)

var bdVersionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+\.[0-9]+\.[0-9]+)(?:[^0-9]|$)`)

type compatibilityKey struct {
	path    string
	size    int64
	modTime int64
}

type compatibilityVerdict struct {
	message string
}

var compatibilityCache = struct {
	sync.Mutex
	results map[compatibilityKey]compatibilityVerdict
}{results: make(map[compatibilityKey]compatibilityVerdict)}

// ensureCompatible rejects bd releases known to migrate databases merely by
// opening them. The probe itself is workspace-independent and runs before the
// caller's bd command. Results are cached by executable identity and mtime.
func ensureCompatible(ctx context.Context, binary string) error {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil // Preserve the caller's normal "bd not found" error path.
	}

	key := compatibilityKey{path: path}
	cacheable := false
	if info, statErr := os.Stat(path); statErr == nil {
		key.size = info.Size()
		key.modTime = info.ModTime().UnixNano()
		cacheable = true
	}

	compatibilityCache.Lock()
	defer compatibilityCache.Unlock()
	if cacheable {
		if verdict, ok := compatibilityCache.results[key]; ok {
			return verdict.err()
		}
	}

	out, probeErr := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if probeErr != nil {
		return fmt.Errorf("cannot verify bd version before workspace access: %w", probeErr)
	}
	version, ok := parseBDVersion(string(out))
	verdict := compatibilityVerdict{}
	if ok && (version == "1.2.0" || version == "1.2.1") {
		verdict.message = fmt.Sprintf("unsafe bd version %s blocked before workspace access; upgrade to bd 1.2.2 or newer", version)
	}
	if cacheable {
		compatibilityCache.results[key] = verdict
	}
	return verdict.err()
}

func parseBDVersion(output string) (string, bool) {
	match := bdVersionPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func (v compatibilityVerdict) err() error {
	if v.message == "" {
		return nil
	}
	return errors.New(v.message)
}
