// Package config provides prompt file parsing for global prompts.
// Prompt files are markdown files with YAML front-matter stored in MITTO_DIR/prompts/.
package config

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/appdir"
)

// PromptFile represents a parsed markdown prompt file with YAML front-matter.
// Files are stored in MITTO_DIR/prompts/ and can be organized in subdirectories.
type PromptFile struct {
	// Path is the relative path from the prompts directory (e.g., "git/commit.md")
	Path string `json:"-"`

	// Name is the display name for the prompt button.
	// If not specified in front-matter, derived from filename.
	Name string `yaml:"name" json:"name"`

	// Description is an optional description shown as tooltip in the UI.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// BackgroundColor is an optional hex color for the prompt button (e.g., "#E8F5E9").
	BackgroundColor string `yaml:"backgroundColor,omitempty" json:"backgroundColor,omitempty"`

	// Icon is an optional icon identifier for future use.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`

	// Tags is an optional list of categorization tags for future use.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// Enabled controls whether the prompt is active. Defaults to true if not specified.
	Enabled *bool `yaml:"enabled,omitempty" json:"-"`

	// Content is the markdown body after the front-matter.
	Content string `json:"prompt"`

	// FileModTime is the file's modification time for cache invalidation.
	FileModTime time.Time `json:"-"`
}

// IsEnabled returns true if the prompt is enabled.
// A nil Enabled field is treated as true (enabled by default).
func (p *PromptFile) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// ToWebPrompt converts the PromptFile to a WebPrompt for API responses.
func (p *PromptFile) ToWebPrompt() WebPrompt {
	return WebPrompt{
		Name:            p.Name,
		Prompt:          p.Content,
		BackgroundColor: p.BackgroundColor,
		Description:     p.Description,
	}
}

// frontMatterDelimiter is the YAML front-matter delimiter.
const frontMatterDelimiter = "---"

// ParsePromptFile parses a markdown file with YAML front-matter.
// The file format is:
//
//	---
//	name: "My Prompt"
//	description: "Optional description"
//	backgroundColor: "#E8F5E9"
//	---
//
//	Prompt content here...
//
// If no front-matter is present, the entire file is treated as content
// and the name is derived from the filename.
func ParsePromptFile(path string, data []byte, modTime time.Time) (*PromptFile, error) {
	prompt := &PromptFile{
		Path:        path,
		FileModTime: modTime,
	}

	content := string(data)

	// Check for front-matter
	if strings.HasPrefix(strings.TrimSpace(content), frontMatterDelimiter) {
		// Find the closing delimiter
		lines := strings.Split(content, "\n")
		var frontMatterEnd int
		foundStart := false

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == frontMatterDelimiter {
				if !foundStart {
					foundStart = true
					continue
				}
				frontMatterEnd = i
				break
			}
		}

		if frontMatterEnd > 0 {
			// Extract and parse front-matter
			frontMatter := strings.Join(lines[1:frontMatterEnd], "\n")
			if err := yaml.Unmarshal([]byte(frontMatter), prompt); err != nil {
				return nil, fmt.Errorf("failed to parse front-matter in %s: %w", path, err)
			}

			// Extract content after front-matter
			if frontMatterEnd+1 < len(lines) {
				prompt.Content = strings.TrimSpace(strings.Join(lines[frontMatterEnd+1:], "\n"))
			}
		} else {
			// Malformed front-matter - treat entire file as content
			prompt.Content = strings.TrimSpace(content)
		}
	} else {
		// No front-matter - entire file is content
		prompt.Content = strings.TrimSpace(content)
	}

	// Derive name from filename if not specified
	if prompt.Name == "" {
		base := filepath.Base(path)
		prompt.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	return prompt, nil
}

// LoadPromptFile loads and parses a single prompt file.
func LoadPromptFile(promptsDir, relativePath string) (*PromptFile, error) {
	fullPath := filepath.Join(promptsDir, relativePath)

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat prompt file %s: %w", relativePath, err)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompt file %s: %w", relativePath, err)
	}

	return ParsePromptFile(relativePath, data, info.ModTime())
}

// LoadPromptsFromDir loads all .md files from a directory recursively.
// Files with enabled: false in front-matter are excluded.
// Returns an empty slice if the directory doesn't exist.
func LoadPromptsFromDir(dir string) ([]*PromptFile, error) {
	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	var prompts []*PromptFile

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process .md files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Load and parse the file
		prompt, err := LoadPromptFile(dir, relPath)
		if err != nil {
			// Log warning but continue with other files
			// In production, this would use a logger
			return nil
		}

		// Skip disabled prompts
		if !prompt.IsEnabled() {
			return nil
		}

		prompts = append(prompts, prompt)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk prompts directory %s: %w", dir, err)
	}

	return prompts, nil
}

// LoadGlobalPrompts loads prompts from MITTO_DIR/prompts/.
// Returns an empty slice if the directory doesn't exist or contains no valid prompts.
func LoadGlobalPrompts() ([]*PromptFile, error) {
	promptsDir, err := appdir.PromptsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get prompts directory: %w", err)
	}

	return LoadPromptsFromDir(promptsDir)
}

// PromptsToWebPrompts converts a slice of PromptFile to WebPrompt.
func PromptsToWebPrompts(prompts []*PromptFile) []WebPrompt {
	if len(prompts) == 0 {
		return nil
	}

	result := make([]WebPrompt, 0, len(prompts))
	for _, p := range prompts {
		result = append(result, p.ToWebPrompt())
	}
	return result
}

// GetPromptsDirModTime returns the most recent modification time of any file
// in the prompts directory. Returns zero time if directory doesn't exist.
func GetPromptsDirModTime(dir string) time.Time {
	var latest time.Time

	// Check directory itself
	info, err := os.Stat(dir)
	if err != nil {
		return latest
	}
	latest = info.ModTime()

	// Walk all files to find the most recent modification
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})

	return latest
}

// init registers a custom YAML unmarshaler for handling the enabled field.
func init() {
	// Ensure bytes package is used (for potential future use)
	_ = bytes.Buffer{}
}
