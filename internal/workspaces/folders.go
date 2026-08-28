package workspaces

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// ShortcutButton describes a single shortcut button shown in a section toolbar.
// Icon is an optional PROMPT_ICONS key (e.g. "lightning"); Prompt is the name
// of the workspace prompt to run when the button is clicked.
type ShortcutButton struct {
	Icon   string `json:"icon" yaml:"icon"`
	Prompt string `json:"prompt" yaml:"prompt"`
}

// TaskLabelColor maps a task label to a task-title background color. Defined
// here (rather than in internal/config) because FolderSettings.TaskLabelColors
// below needs it and internal/config already imports internal/workspaces, so
// the reverse import would be circular. internal/config re-exports this as an
// alias (see workspaces_shim.go), mirroring ShortcutButton.
type TaskLabelColor struct {
	Label string `json:"label" yaml:"label"`
	Color string `json:"color" yaml:"color"`
}

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
	// Shortcuts holds per-section configurable shortcut buttons, keyed by section
	// ID (e.g. "tasksList") so new sections need no schema change. Folder-native:
	// preserved across workspace-driven saves by preserveFolderNativeFields.
	Shortcuts map[string][]ShortcutButton `json:"shortcuts,omitempty" yaml:"shortcuts,omitempty"`
	// TaskLabelColors is an ordered folder-level mapping from task labels to
	// task-title background colors, merged with the global mapping at render
	// time (folder entries first). Folder-native: no per-workspace counterpart;
	// preserved across workspace-driven saves by preserveFolderNativeFields.
	TaskLabelColors []TaskLabelColor `json:"task_label_colors,omitempty" yaml:"task_label_colors,omitempty"`
	// Pinned is a folder-level visibility flag. When true the folder is shown in
	// the sidebar even if it has no conversations. Folder-native: no per-workspace
	// counterpart and preserved across workspace-driven saves by
	// preserveFolderNativeFields.
	Pinned bool `json:"pinned,omitempty" yaml:"pinned,omitempty"`
	// LastOpenedAt records when the folder was most recently opened/pinned or
	// received a new session, used by the "Add folder" dialog to sort hidden
	// workspaces MRU-first. Folder-native: no per-workspace counterpart; preserved
	// across workspace-driven saves by preserveFolderNativeFields.
	LastOpenedAt time.Time `json:"last_opened_at,omitempty" yaml:"last_opened_at,omitempty"`
}

// BeadsFolderSettings holds folder-native beads integration settings.
type BeadsFolderSettings struct {
	// DatabaseMode controls whether the folder's Beads Dolt database is local-only
	// or shared through a configured remote. It is independent from Upstream,
	// which selects an external task system. Empty is a legacy/unresolved value.
	DatabaseMode BeadsDatabaseMode `json:"databaseMode,omitempty" yaml:"databaseMode,omitempty"`
	// Upstream selects the external task system beads syncs with. One of
	// "jira", "github", "gitlab", "linear", or "prompts". An empty value (or the
	// absence of the Beads block) means no upstream is configured ("none").
	Upstream string `json:"upstream,omitempty" yaml:"upstream,omitempty"`
	// PullPrompt is the name of the workspace prompt to run for a pull operation.
	// Only meaningful when Upstream == "prompts".
	PullPrompt string `json:"pullPrompt,omitempty" yaml:"pullPrompt,omitempty"`
	// PushPrompt is the name of the workspace prompt to run for a push operation.
	// Only meaningful when Upstream == "prompts".
	PushPrompt string `json:"pushPrompt,omitempty" yaml:"pushPrompt,omitempty"`
	// SyncPrompt is the name of the workspace prompt to run for a sync operation.
	// Only meaningful when Upstream == "prompts".
	SyncPrompt string `json:"syncPrompt,omitempty" yaml:"syncPrompt,omitempty"`
	// PullPromptArgs, PushPromptArgs, SyncPromptArgs hold the argument values to
	// forward to the corresponding prompt when it is dispatched. Only meaningful
	// when Upstream == "prompts".
	PullPromptArgs map[string]string `json:"pullPromptArgs,omitempty" yaml:"pullPromptArgs,omitempty"`
	PushPromptArgs map[string]string `json:"pushPromptArgs,omitempty" yaml:"pushPromptArgs,omitempty"`
	SyncPromptArgs map[string]string `json:"syncPromptArgs,omitempty" yaml:"syncPromptArgs,omitempty"`
}

// BeadsDatabaseMode is the folder-native policy for Beads Dolt replication.
type BeadsDatabaseMode string

const (
	BeadsDatabaseModeLocal  BeadsDatabaseMode = "local"
	BeadsDatabaseModeShared BeadsDatabaseMode = "shared"
)

// IsValidBeadsDatabaseMode reports whether mode is one of the persisted values.
func IsValidBeadsDatabaseMode(mode BeadsDatabaseMode) bool {
	return mode == BeadsDatabaseModeLocal || mode == BeadsDatabaseModeShared
}

func validateFolderBeadsModes(folders map[string]FolderSettings) error {
	for workingDir, fs := range folders {
		if fs.Beads == nil || fs.Beads.DatabaseMode == "" {
			continue
		}
		if !IsValidBeadsDatabaseMode(fs.Beads.DatabaseMode) {
			return fmt.Errorf("folder %q has invalid beads database mode %q", workingDir, fs.Beads.DatabaseMode)
		}
	}
	return nil
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
	if err := validateFolderBeadsModes(file.Folders); err != nil {
		return nil, err
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
	if err := validateFolderBeadsModes(file.Folders); err != nil {
		return nil, err
	}
	return file.Folders, nil
}

// SaveFolders writes folder-level settings to $MITTO_DIR/folders.json.
// When the map is empty, any existing folders.json is removed to keep the
// data directory clean (an empty folders.json carries no information).
func SaveFolders(folders map[string]FolderSettings) error {
	if err := validateFolderBeadsModes(folders); err != nil {
		return err
	}
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
		workspaces[i].Pinned = fs.Pinned
		workspaces[i].LastOpenedAt = fs.LastOpenedAt
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
		// Pinned is folder-native (never hoisted from workspaces) but still stripped
		// here because ApplyFolderDefaults injects it as a read-only projection on
		// load; it is (re)injected into the folders map by preserveFolderNativeFields.
		for _, i := range idxs {
			cleaned[i].Name = ""
			cleaned[i].Color = ""
			cleaned[i].Code = ""
			cleaned[i].Group = ""
			cleaned[i].AutoChildren = nil
			cleaned[i].Pinned = false
			cleaned[i].LastOpenedAt = time.Time{}
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
		if av.Pinned != bv.Pinned {
			return false
		}
		if !av.LastOpenedAt.Equal(bv.LastOpenedAt) {
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
	return a.DatabaseMode == b.DatabaseMode &&
		a.Upstream == b.Upstream &&
		a.PullPrompt == b.PullPrompt &&
		a.PushPrompt == b.PushPrompt &&
		a.SyncPrompt == b.SyncPrompt &&
		maps.Equal(a.PullPromptArgs, b.PullPromptArgs) &&
		maps.Equal(a.PushPromptArgs, b.PushPromptArgs) &&
		maps.Equal(a.SyncPromptArgs, b.SyncPromptArgs)
}

// folderSettingsEmpty reports whether a FolderSettings carries no information
// and can therefore be dropped from folders.json.
func folderSettingsEmpty(fs FolderSettings) bool {
	if fs.Name != "" || fs.Color != "" || fs.Code != "" || fs.Group != "" {
		return false
	}
	if len(fs.AutoChildren) > 0 {
		return false
	}
	if !beadsSettingsEmpty(fs.Beads) {
		return false
	}
	if fs.Pinned {
		return false
	}
	if !fs.LastOpenedAt.IsZero() {
		return false
	}
	for _, buttons := range fs.Shortcuts {
		if len(buttons) > 0 {
			return false
		}
	}
	if len(fs.TaskLabelColors) > 0 {
		return false
	}
	return true
}

func beadsSettingsEmpty(settings *BeadsFolderSettings) bool {
	return settings == nil || (settings.DatabaseMode == "" && settings.Upstream == "" &&
		settings.PullPrompt == "" && settings.PushPrompt == "" && settings.SyncPrompt == "" &&
		len(settings.PullPromptArgs) == 0 && len(settings.PushPromptArgs) == 0 && len(settings.SyncPromptArgs) == 0)
}

// preserveFolderNativeFields merges folder-native settings (those not derived
// from workspaces: Beads, Shortcuts, Pinned) from the authoritative on-disk
// folders.json into the freshly extracted folders map. extractFolderSettings
// only produces workspace-derived fields (name/color/code/auto_children), so
// without this merge a workspace-driven save would wipe the folder-native
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
		if !valid[wd] {
			continue
		}
		hasBeads := !beadsSettingsEmpty(ex.Beads)
		hasShortcuts := false
		for _, buttons := range ex.Shortcuts {
			if len(buttons) > 0 {
				hasShortcuts = true
				break
			}
		}
		hasTaskLabelColors := len(ex.TaskLabelColors) > 0
		hasPinned := ex.Pinned
		hasLastOpenedAt := !ex.LastOpenedAt.IsZero()
		if !hasBeads && !hasShortcuts && !hasTaskLabelColors && !hasPinned && !hasLastOpenedAt {
			continue
		}
		if out == nil {
			out = map[string]FolderSettings{}
		}
		fs := out[wd]
		if hasBeads {
			fs.Beads = ex.Beads
		}
		if hasShortcuts {
			fs.Shortcuts = ex.Shortcuts
		}
		if hasTaskLabelColors {
			fs.TaskLabelColors = ex.TaskLabelColors
		}
		if hasPinned {
			fs.Pinned = true
		}
		if hasLastOpenedAt {
			fs.LastOpenedAt = ex.LastOpenedAt
		}
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
	if fs.Beads == nil {
		fs.Beads = &BeadsFolderSettings{}
	}
	fs.Beads.PullPrompt = ""
	fs.Beads.PushPrompt = ""
	fs.Beads.SyncPrompt = ""
	fs.Beads.PullPromptArgs = nil
	fs.Beads.PushPromptArgs = nil
	fs.Beads.SyncPromptArgs = nil
	if upstream == "" || upstream == "none" {
		fs.Beads.Upstream = ""
	} else {
		fs.Beads.Upstream = upstream
	}
	if beadsSettingsEmpty(fs.Beads) {
		fs.Beads = nil
	}
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// SetFolderBeadsPromptUpstream sets the beads upstream to "prompts" and
// persists the three configured prompt names (and their argument maps) to
// folders.json. Empty prompt names are allowed (the corresponding operation is
// simply unconfigured). This is a folder-native field, preserved across
// workspace-driven saves by preserveFolderNativeFields.
func SetFolderBeadsPromptUpstream(workingDir, pull, push, sync string, pullArgs, pushArgs, syncArgs map[string]string) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	if fs.Beads == nil {
		fs.Beads = &BeadsFolderSettings{}
	}
	fs.Beads.Upstream = "prompts"
	fs.Beads.PullPrompt = pull
	fs.Beads.PushPrompt = push
	fs.Beads.SyncPrompt = sync
	fs.Beads.PullPromptArgs = pullArgs
	fs.Beads.PushPromptArgs = pushArgs
	fs.Beads.SyncPromptArgs = syncArgs
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// ConfiguredFolderBeadsDatabaseMode returns the explicit folders.json policy.
// configured is false for legacy folders that have not yet been inferred.
func ConfiguredFolderBeadsDatabaseMode(workingDir string) (mode BeadsDatabaseMode, configured bool, err error) {
	folders, err := LoadFolders()
	if err != nil {
		return "", false, err
	}
	fs, ok := folders[workingDir]
	if !ok || fs.Beads == nil || fs.Beads.DatabaseMode == "" {
		return "", false, nil
	}
	return fs.Beads.DatabaseMode, true, nil
}

// SetFolderBeadsDatabaseMode persists the folder policy without changing the
// external-task upstream or its prompt arguments.
func SetFolderBeadsDatabaseMode(workingDir string, mode BeadsDatabaseMode) error {
	if !IsValidBeadsDatabaseMode(mode) {
		return fmt.Errorf("invalid beads database mode %q", mode)
	}
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	if fs.Beads == nil {
		fs.Beads = &BeadsFolderSettings{}
	}
	fs.Beads.DatabaseMode = mode
	folders[workingDir] = fs
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

// FolderBeadsPrompts returns the three configured prompt names for the "prompts"
// upstream of a folder. Returns empty strings if none are set or folders.json
// cannot be read.
func FolderBeadsPrompts(workingDir string) (pull, push, sync string) {
	folders, err := LoadFolders()
	if err != nil {
		return "", "", ""
	}
	fs, ok := folders[workingDir]
	if !ok || fs.Beads == nil {
		return "", "", ""
	}
	return fs.Beads.PullPrompt, fs.Beads.PushPrompt, fs.Beads.SyncPrompt
}

// FolderBeadsPromptArgs returns the three configured prompt argument maps for
// the "prompts" upstream of a folder. Returns nil maps if none are set or
// folders.json cannot be read.
func FolderBeadsPromptArgs(workingDir string) (pull, push, sync map[string]string) {
	folders, err := LoadFolders()
	if err != nil {
		return nil, nil, nil
	}
	fs, ok := folders[workingDir]
	if !ok || fs.Beads == nil {
		return nil, nil, nil
	}
	return fs.Beads.PullPromptArgs, fs.Beads.PushPromptArgs, fs.Beads.SyncPromptArgs
}

// FolderShortcuts returns the configured shortcut sections for a folder, or nil.
func FolderShortcuts(workingDir string) map[string][]ShortcutButton {
	folders, err := LoadFolders()
	if err != nil || folders == nil {
		return nil
	}
	fs, ok := folders[workingDir]
	if !ok {
		return nil
	}
	return fs.Shortcuts
}

// SetFolderShortcuts persists shortcut sections to folders.json. Empty/absent
// sections are pruned; if the folder becomes empty its entry is removed.
func SetFolderShortcuts(workingDir string, sections map[string][]ShortcutButton) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	// Prune sections with no buttons.
	cleaned := map[string][]ShortcutButton{}
	for k, v := range sections {
		if len(v) > 0 {
			cleaned[k] = v
		}
	}
	if len(cleaned) == 0 {
		fs.Shortcuts = nil
	} else {
		fs.Shortcuts = cleaned
	}
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// FolderTaskLabelColors returns the configured ordered task-label color
// mapping for a folder, or nil.
func FolderTaskLabelColors(workingDir string) []TaskLabelColor {
	folders, err := LoadFolders()
	if err != nil || folders == nil {
		return nil
	}
	fs, ok := folders[workingDir]
	if !ok {
		return nil
	}
	return fs.TaskLabelColors
}

// SetFolderTaskLabelColors persists the ordered task-label color mapping to
// folders.json. An empty/nil slice clears it; if the folder becomes empty its
// entry is removed.
func SetFolderTaskLabelColors(workingDir string, entries []TaskLabelColor) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	if len(entries) == 0 {
		fs.TaskLabelColors = nil
	} else {
		fs.TaskLabelColors = entries
	}
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// SetFolderPinned sets (or clears) the folder-level visibility (pinned) flag,
// persisting it directly to folders.json. This is a folder-native field,
// preserved across workspace-driven saves by preserveFolderNativeFields.
func SetFolderPinned(workingDir string, pinned bool) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	fs.Pinned = pinned
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// FolderPinned returns whether a folder is marked as pinned (visible in the
// sidebar even when it has no conversations). Returns false if not set or on
// read error.
func FolderPinned(workingDir string) bool {
	folders, err := LoadFolders()
	if err != nil {
		return false
	}
	fs, ok := folders[workingDir]
	if !ok {
		return false
	}
	return fs.Pinned
}

// SetFolderLastOpenedAt stamps the folder's last-opened timestamp in
// folders.json. Callers use this to record folder activity (pinning or a new
// session) so the "Add folder" dialog can rank hidden folders MRU-first.
func SetFolderLastOpenedAt(workingDir string, t time.Time) error {
	folders, err := LoadFolders()
	if err != nil {
		return err
	}
	if folders == nil {
		folders = map[string]FolderSettings{}
	}
	fs := folders[workingDir]
	fs.LastOpenedAt = t
	if folderSettingsEmpty(fs) {
		delete(folders, workingDir)
	} else {
		folders[workingDir] = fs
	}
	return SaveFolders(folders)
}

// FolderLastOpenedAt returns the folder's last-opened timestamp, or the zero
// time if unset or on read error.
func FolderLastOpenedAt(workingDir string) time.Time {
	folders, err := LoadFolders()
	if err != nil {
		return time.Time{}
	}
	fs, ok := folders[workingDir]
	if !ok {
		return time.Time{}
	}
	return fs.LastOpenedAt
}
