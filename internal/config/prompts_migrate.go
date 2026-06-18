// Package config: migration of legacy markdown prompt files to the new
// .prompt.yaml format. Legacy files use YAML front-matter delimited by "---"
// followed by a markdown body that becomes the prompt content. The new format
// is a single YAML document with the body stored under the "prompt" key.
package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// frontMatterDelimiter separates the YAML front-matter from the body in legacy
// markdown prompt files.
const frontMatterDelimiter = "---"

// legacyPromptExt is the file extension of legacy markdown prompt files.
const legacyPromptExt = ".md"

// newPromptExt is the file extension of the current prompt file format.
const newPromptExt = ".prompt.yaml"

// MigratedPrompt records a single legacy prompt file that was migrated to the
// new .prompt.yaml format.
type MigratedPrompt struct {
	// Name is the resulting prompt's display name.
	Name string
	// SourcePath is the absolute path of the legacy .md file (left untouched).
	SourcePath string
	// TargetPath is the absolute path of the newly written .prompt.yaml file.
	TargetPath string
}

// parseLegacyMarkdownPrompt parses a legacy markdown prompt file with optional
// YAML front-matter. If no front-matter is present, the entire file is treated
// as the prompt body and the name is derived from the filename.
func parseLegacyMarkdownPrompt(path string, data []byte, modTime time.Time) (*PromptFile, error) {
	prompt := &PromptFile{Path: path, FileModTime: modTime}
	content := string(data)

	if strings.HasPrefix(strings.TrimSpace(content), frontMatterDelimiter) {
		lines := strings.Split(content, "\n")
		var frontMatterEnd int
		foundStart := false
		for i, line := range lines {
			if strings.TrimSpace(line) == frontMatterDelimiter {
				if !foundStart {
					foundStart = true
					continue
				}
				frontMatterEnd = i
				break
			}
		}

		if frontMatterEnd > 0 {
			frontMatter := strings.Join(lines[1:frontMatterEnd], "\n")
			if err := yaml.Unmarshal([]byte(frontMatter), prompt); err != nil {
				return nil, fmt.Errorf("failed to parse front-matter in %s: %w", path, err)
			}
			if frontMatterEnd+1 < len(lines) {
				prompt.Content = strings.TrimSpace(strings.Join(lines[frontMatterEnd+1:], "\n"))
			}
		} else {
			// Malformed front-matter — treat the whole file as content.
			prompt.Content = strings.TrimSpace(content)
		}
	} else {
		prompt.Content = strings.TrimSpace(content)
	}

	if prompt.Name == "" {
		base := filepath.Base(path)
		prompt.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return prompt, nil
}

// legacyTargetPath returns the .prompt.yaml path corresponding to a legacy .md
// path (same directory, base name with the extension swapped).
func legacyTargetPath(mdPath string) string {
	return strings.TrimSuffix(mdPath, legacyPromptExt) + newPromptExt
}

// marshalPromptFile serializes a PromptFile to YAML, preferring a readable
// literal block scalar ("prompt: |-") for the multi-line body instead of the
// escaped double-quoted scalar that yaml.v3 emits by default. This keeps
// migrated files looking like hand-authored prompts.
//
// Note: yaml.v3 (v3.0.1) refuses to emit a literal block whenever the body
// contains a 4-byte UTF-8 rune (e.g. emoji), because its is_printable check only
// covers the BMP; such runes set special_characters and disable block style.
// Setting LiteralStyle on the node is therefore silently ignored. To produce a
// human-readable body we hand-build the literal block and verify it round-trips;
// if it does not (or the body is single-line / empty), we fall back to the
// canonical yaml.Marshal encoding, which is always lossless.
func marshalPromptFile(prompt *PromptFile) ([]byte, error) {
	canonical, err := yaml.Marshal(prompt)
	if err != nil {
		return nil, err
	}
	if literal, ok := marshalPromptFileLiteral(prompt); ok {
		return literal, nil
	}
	return canonical, nil
}

// marshalPromptFileLiteral hand-builds a YAML document where the "prompt" body
// is a literal block scalar. It returns (nil, false) when a literal block is not
// applicable (single-line or empty body) or when the result does not round-trip
// back to the original content, signalling the caller to use the canonical
// encoding instead.
func marshalPromptFileLiteral(prompt *PromptFile) ([]byte, bool) {
	if !strings.Contains(prompt.Content, "\n") {
		return nil, false
	}

	// Marshal the metadata with an empty body so yaml handles quoting/escaping
	// of every other field, then strip the placeholder "prompt" line and append
	// the hand-built literal block.
	meta := *prompt
	meta.Content = ""
	metaBytes, err := yaml.Marshal(&meta)
	if err != nil {
		return nil, false
	}
	metaStr := strings.TrimSuffix(string(metaBytes), "prompt: \"\"\n")
	if strings.Contains(metaStr, "\nprompt:") || strings.HasPrefix(metaStr, "prompt:") {
		// Placeholder line wasn't where we expected; bail out to stay safe.
		return nil, false
	}

	var b strings.Builder
	b.WriteString(metaStr)
	b.WriteString("prompt: |-\n")
	const indent = "  "
	for _, line := range strings.Split(prompt.Content, "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := []byte(b.String())

	// Safety: only use the literal block if it parses back to the same content.
	var rt PromptFile
	if err := yaml.Unmarshal(out, &rt); err != nil {
		return nil, false
	}
	if rt.Content != prompt.Content {
		return nil, false
	}
	return out, true
}

// MigrateMarkdownPromptsInDir scans dir recursively for legacy .md prompt files
// and writes an equivalent .prompt.yaml file next to each one when the target
// does not already exist. The original .md files are never modified or removed,
// so migration is idempotent: once a .prompt.yaml exists, the .md is skipped.
// Returns the list of files migrated by this call. A non-existent directory is
// treated as empty (no error).
func MigrateMarkdownPromptsInDir(dir string) ([]MigratedPrompt, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	var migrated []MigratedPrompt

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), legacyPromptExt) {
			return nil
		}

		targetPath := legacyTargetPath(path)
		// Skip if already migrated (target exists).
		if _, statErr := os.Stat(targetPath); statErr == nil {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		prompt, parseErr := parseLegacyMarkdownPrompt(path, data, info.ModTime())
		if parseErr != nil {
			return nil
		}

		out, marshalErr := marshalPromptFile(prompt)
		if marshalErr != nil {
			return nil
		}
		if writeErr := os.WriteFile(targetPath, out, 0644); writeErr != nil {
			return nil
		}

		migrated = append(migrated, MigratedPrompt{
			Name:       prompt.Name,
			SourcePath: path,
			TargetPath: targetPath,
		})
		return nil
	})

	if walkErr != nil {
		return migrated, fmt.Errorf("failed to walk prompts directory %s: %w", dir, walkErr)
	}
	return migrated, nil
}
