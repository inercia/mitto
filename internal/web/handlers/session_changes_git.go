package handlers

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// classifyGitStatus determines the single-letter status from porcelain format.
func classifyGitStatus(indexStatus, workTreeStatus byte) string {
	switch {
	case indexStatus == '?' && workTreeStatus == '?':
		return "?"
	case indexStatus == 'A' || (indexStatus == ' ' && workTreeStatus == 'A'):
		return "A"
	case indexStatus == 'D' || workTreeStatus == 'D':
		return "D"
	case indexStatus == 'R':
		return "R"
	case indexStatus == 'C':
		return "C"
	default:
		return "M"
	}
}

// mergeNumstat runs git diff with numstat and merges additions/deletions into the file map.
func mergeNumstat(ctx context.Context, workDir string, fileMap map[string]*ChangedFile, ref, flag string) {
	args := []string{"diff", "--no-ext-diff", "--no-color", ref, flag}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		filePath := parts[2]
		if len(parts) > 3 {
			filePath = strings.Join(parts[2:], " ")
		}
		if strings.Contains(filePath, " => ") {
			for mapPath := range fileMap {
				if strings.HasSuffix(filePath, mapPath) || mapPath == filePath {
					filePath = mapPath
					break
				}
			}
		}
		if cf, ok := fileMap[filePath]; ok {
			if adds, err := strconv.Atoi(parts[0]); err == nil {
				cf.Additions += adds
			}
			if dels, err := strconv.Atoi(parts[1]); err == nil {
				cf.Deletions += dels
			}
		}
	}
}
