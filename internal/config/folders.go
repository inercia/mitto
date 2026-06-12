package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// FolderSettings holds folder-level settings shared by all workspaces that
// operate on the same working directory. folders.json is the AUTHORITATIVE store
// for these values: they live here once per folder rather than being repeated on
// every workspace entry. The file is created the first time via a one-time
// migration that lifts any inline folder fields out of workspaces.json (see
// LoadWorkspaces); thereafter all common folder-level information always lives
// here.
//
// Only badge/display fields, the group label, and auto-children are stored
// here. The project-level .mittorc metadata block (description/url/group/
// user_data_schema) is a SEPARATE, version-controllable concept and is NOT
// stored here. In particular, the folder Group below (a Mitto-local
// organizational label kept in folders.json) is distinct from the .mittorc
// metadata group.
type FolderSettings struct {
	// Name is the friendly display name for the folder.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// Color is the custom badge color for the folder (e.g. "#ff5500").
	Color string `json:"color,omitempty" yaml:"color,omitempty"`
	// Code is the three-letter badge code for the folder.
	Code string `json:"code,omitempty" yaml:"code,omitempty"`
	// Group is an optional organizational label for the folder (e.g.
	// "development", "personal", "operations"). It is Mitto-local (kept in
	// folders.json, not committed) and lets the UI group folders together.
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
	// AutoChildren defines child conversations to auto-create for the folder.
	AutoChildren []AutoChild `json:"auto_children,omitempty" yaml:"auto_children,omitempty"`
	// Beads holds folder-native beads integration settings. These are
	// folder-native: unlike the other fields they have no per-workspace
	// counterpart and are written directly to folders.json (never lifted from a
	// workspace), so they are merged back on every workspace-driven save by
	// preserveFolderNativeFields.
	Beads *BeadsFolderSettings `json:"beads,omitempty" yaml:"beads,omitempty"`
}

// BeadsFolderSettings holds folder-native beads integration settings.
type BeadsFolderSettings struct {
	// Upstream selects the external task system beads syncs with. One of
	// "jira", "github", "gitlab", or "linear". An empty value (or the absence of the
	// Beads block) means no upstream is configured ("none").
	Upstream string `json:"upstream,omitempty" yaml:"upstream,omitempty"`
}

// FoldersFile is the on-disk representation of folders.json. It maps a working
// directory (absolute path) to its folder-level settings.
type FoldersFile struct {
	Folders map[string]FolderSettings `json:"folders" yaml:"folders"`
}

// LoadFolders loads folder-level settings from $MITTO_DIR/folders.json.
// Returns nil (not an error) if the file does not exist.
func LoadFolders() (map[string]FolderSettings, error) {
	path, err := appdir.FoldersPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	var file FoldersFile
	if err := fileutil.ReadJSON(path, &file); err != nil {
		return nil, err
	}
	if file.Folders == nil {
		file.Folders = map[string]FolderSettings{}
	}
	return file.Folders, nil
}

// LoadFoldersFromFile loads folder-level settings from an explicit JSON or YAML
// file. The format is detected by file extension: .json → JSON, .yaml/.yml →
// YAML. Returns an error for unsupported extensions or file read/parse
// failures. The file is NOT modified.
func LoadFoldersFromFile(path string) (map[string]FolderSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read folders file %s: %w", path, err)
	}

	var file FoldersFile
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("failed to parse JSON folders file %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("failed to parse YAML folders file %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported folders file extension %q: must be .json, .yaml, or .yml", ext)
	}

	if file.Folders == nil {
		file.Folders = map[string]FolderSettings{}
	}
	return file.Folders, nil
}

// SaveFolders writes folder-level settings to $MITTO_DIR/folders.json.
// When the map is empty, any existing folders.json is removed to keep the
// data directory clean (an empty folders.json carries no information).
func SaveFolders(folders map[string]FolderSettings) error {
	path, err := appdir.FoldersPath()
	if err != nil {
		return err
	}
	if len(folders) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		return nil
	}
	return fileutil.WriteJSONAtomic(path, FoldersFile{Folders: folders}, 0644)
}

// ApplyFolderDefaults populates folder-level fields on each workspace from the
// authoritative folders map. Because folders.json is authoritative, a non-empty
// folder value OVERWRITES any value still present on the workspace, collapsing
// divergent legacy values onto the single folder value. Fields absent from the
// folder entry fall back to the workspace's own value. Operates in place.
func ApplyFolderDefaults(workspaces []WorkspaceSettings, folders map[string]FolderSettings) {
	if len(folders) == 0 {
		return
	}
	for i := range workspaces {
		fs, ok := folders[workspaces[i].WorkingDir]
		if !ok {
			continue
		}
		if fs.Name != "" {
			workspaces[i].Name = fs.Name
		}
		if fs.Color != "" {
			workspaces[i].Color = fs.Color
		}
		if fs.Code != "" {
			workspaces[i].Code = fs.Code
		}
		if fs.Group != "" {
			workspaces[i].Group = fs.Group
		}
		if len(fs.AutoChildren) > 0 {
			workspaces[i].AutoChildren = append([]AutoChild(nil), fs.AutoChildren...)
		}
	}
}

// extractFolderSettings takes a fully-populated workspace list and splits the
// folder-level fields (name, color, code, group, auto_children) out into an
// authoritative folders map keyed by working_dir, returning a cleaned copy of
// the workspaces with those fields removed.
//
// Because folders.json is the authoritative home for folder-level settings, a
// field is ALWAYS hoisted whenever any workspace in the folder group carries a
// non-empty value: the first non-empty value (in workspace order) wins and any
// divergent values collapse onto it. The field is then stripped from every
// workspace in the group, so it lives solely in folders.json. Workspaces with an
// empty WorkingDir (e.g. the CLI default workspace) are never extracted.
// Original ordering is preserved.
func extractFolderSettings(workspaces []WorkspaceSettings) ([]WorkspaceSettings, map[string]FolderSettings) {
	cleaned := make([]WorkspaceSettings, len(workspaces))
	copy(cleaned, workspaces)

	groups := map[string][]int{}
	order := []string{}
	for i := range cleaned {
		wd := cleaned[i].WorkingDir
		if wd == "" {
			continue
		}
		if _, ok := groups[wd]; !ok {
			order = append(order, wd)
		}
		groups[wd] = append(groups[wd], i)
	}

	folders := map[string]FolderSettings{}
	for _, wd := range order {
		idxs := groups[wd]
		var fs FolderSettings
		any := false

		if v := firstNonEmptyString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Name }); v != "" {
			fs.Name = v
			any = true
		}
		if v := firstNonEmptyString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Color }); v != "" {
			fs.Color = v
			any = true
		}
		if v := firstNonEmptyString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Code }); v != "" {
			fs.Code = v
			any = true
		}
		if v := firstNonEmptyString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Group }); v != "" {
			fs.Group = v
			any = true
		}
		if v := firstNonEmptyAutoChildren(cleaned, idxs); len(v) > 0 {
			fs.AutoChildren = v
			any = true
		}

		// Folder-level fields always live in folders.json (authoritative), so
		// strip them from every workspace in the group regardless of divergence.
		for _, i := range idxs {
			cleaned[i].Name = ""
			cleaned[i].Color = ""
			cleaned[i].Code = ""
			cleaned[i].Group = ""
			cleaned[i].AutoChildren = nil
		}

		if any {
			folders[wd] = fs
		}
	}
	return cleaned, folders
}

// firstNonEmptyString returns the first non-empty value produced by get across
// the given workspace indices, or "" if all are empty.
func firstNonEmptyString(ws []WorkspaceSettings, idxs []int, get func(*WorkspaceSettings) string) string {
	for _, i := range idxs {
		if v := get(&ws[i]); v != "" {
			return v
		}
	}
	return ""
}

// firstNonEmptyAutoChildren returns a copy of the first non-empty AutoChildren
// slice across the given workspace indices, or nil if all are empty.
func firstNonEmptyAutoChildren(ws []WorkspaceSettings, idxs []int) []AutoChild {
	for _, i := range idxs {
		if len(ws[i].AutoChildren) > 0 {
			out := make([]AutoChild, len(ws[i].AutoChildren))
			copy(out, ws[i].AutoChildren)
			return out
		}
	}
	return nil
}

func autoChildrenEqual(a, b []AutoChild) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// foldersEqual reports whether two folder maps are equivalent, treating a nil
// map and an empty map as equal.
func foldersEqual(a, b map[string]FolderSettings) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av.Name != bv.Name || av.Color != bv.Color || av.Code != bv.Code || av.Group != bv.Group {
			return false
		}
		if !autoChildrenEqual(av.AutoChildren, bv.AutoChildren) {
			return false
		}
		if !beadsEqual(av.Beads, bv.Beads) {
			return false
		}
	}
	return true
}

// beadsEqual reports whether two beads settings pointers are equivalent,
// treating nil as "no upstream configured".
func beadsEqual(a, b *BeadsFolderSettings) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Upstream == b.Upstream
}

// folderSettingsEmpty reports whether a FolderSettings carries no information
// and can therefore be dropped from folders.json.
func folderSettingsEmpty(fs FolderSettings) bool {
	return fs.Name == "" && fs.Color == "" && fs.Code == "" && fs.Group == "" &&
		len(fs.AutoChildren) == 0 && (fs.Beads == nil || fs.Beads.Upstream == "")
}

// preserveFolderNativeFields merges folder-native settings (those not derived
// from workspaces, currently only Beads) from the authoritative on-disk
// folders.json into the freshly extracted folders map. extractFolderSettings
// only produces workspace-derived fields (name/color/code/auto_children), so
// without this merge a workspace-driven save would wipe the folder-native beads
// settings that live solely in folders.json. Only folders whose working_dir is
// still referenced by a current workspace are preserved; orphaned folder entries
// are dropped (matching the extraction pruning behaviour).
func preserveFolderNativeFields(workspaces []WorkspaceSettings, folders map[string]FolderSettings) map[string]FolderSettings {
	existing, err := LoadFolders()
	if err != nil || len(existing) == 0 {
		return folders
	}
	valid := map[string]bool{}
	for i := range workspaces {
		if workspaces[i].WorkingDir != "" {
			valid[workspaces[i].WorkingDir] = true
		}
	}
	out := folders
	for wd, ex := range existing {
		if ex.Beads == nil || ex.Beads.Upstream == "" || !valid[wd] {
			continue
		}
		if out == nil {
			out = map[string]FolderSettings{}
		}
		fs := out[wd]
		fs.Beads = ex.Beads
		out[wd] = fs
	}
	return out
}

// SetFolderBeadsUpstream sets (or clears) the beads upstream task system for a
// folder, persisting it directly to folders.json. An upstream of "" or "none"
// clears the setting. This is a folder-native field, preserved across
// workspace-driven saves by preserveFolderNativeFields.
func SetFolderBeadsUpstream(workingDir, upstream string) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	if upstream == "" || upstream == "none" {
		fs.Beads = nil
	} else {
		fs.Beads = &BeadsFolderSettings{Upstream: upstream}
	}
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// FolderBeadsUpstream returns the configured beads upstream for a folder, or
// "" if none is set or folders.json cannot be read.
func FolderBeadsUpstream(workingDir string) string {
	folders, err := LoadFolders()
	if err != nil {
		return ""
	}
	fs, ok := folders[workingDir]
	if !ok || fs.Beads == nil {
		return ""
	}
	return fs.Beads.Upstream
}
