package config

import (
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// FolderSettings holds folder-level settings shared by all workspaces that
// operate on the same working directory. These values are deduplicated out of
// workspaces.json (where they would otherwise be repeated on every workspace
// entry for the folder) and stored once per folder in folders.json.
//
// Only badge/display fields and auto-children are stored here. Workspace
// metadata (description/url/group/user_data_schema), prompts, and processors
// remain in each project's committable .mittorc file and are NOT stored here.
type FolderSettings struct {
	// Name is the friendly display name for the folder.
	Name string `json:"name,omitempty"`
	// Color is the custom badge color for the folder (e.g. "#ff5500").
	Color string `json:"color,omitempty"`
	// Code is the three-letter badge code for the folder.
	Code string `json:"code,omitempty"`
	// AutoChildren defines child conversations to auto-create for the folder.
	AutoChildren []AutoChild `json:"auto_children,omitempty"`
}

// FoldersFile is the on-disk representation of folders.json. It maps a working
// directory (absolute path) to its folder-level settings.
type FoldersFile struct {
	Folders map[string]FolderSettings `json:"folders"`
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

// applyFolderDefaults populates empty folder-level fields on each workspace
// from the folders map. A workspace's own non-empty value always takes
// precedence over the folder default (this preserves divergent legacy values
// that were not deduplicated). Operates in place.
func applyFolderDefaults(workspaces []WorkspaceSettings, folders map[string]FolderSettings) {
	if len(folders) == 0 {
		return
	}
	for i := range workspaces {
		fs, ok := folders[workspaces[i].WorkingDir]
		if !ok {
			continue
		}
		if workspaces[i].Name == "" {
			workspaces[i].Name = fs.Name
		}
		if workspaces[i].Color == "" {
			workspaces[i].Color = fs.Color
		}
		if workspaces[i].Code == "" {
			workspaces[i].Code = fs.Code
		}
		if len(workspaces[i].AutoChildren) == 0 && len(fs.AutoChildren) > 0 {
			workspaces[i].AutoChildren = append([]AutoChild(nil), fs.AutoChildren...)
		}
	}
}

// dedupFolders takes a fully-populated workspace list and returns a cleaned
// copy with folder-level fields removed where they are identical across all
// workspaces that share a working_dir, plus a folders map capturing those
// hoisted values. A field is hoisted only when every workspace in the folder
// group shares the same non-empty value; divergent values are left in place on
// the individual workspaces. Workspaces with an empty WorkingDir (e.g. the CLI
// default workspace) are never deduplicated. Original ordering is preserved.
func dedupFolders(workspaces []WorkspaceSettings) ([]WorkspaceSettings, map[string]FolderSettings) {
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

		if v, ok := allSameString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Name }); ok && v != "" {
			fs.Name = v
			any = true
			for _, i := range idxs {
				cleaned[i].Name = ""
			}
		}
		if v, ok := allSameString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Color }); ok && v != "" {
			fs.Color = v
			any = true
			for _, i := range idxs {
				cleaned[i].Color = ""
			}
		}
		if v, ok := allSameString(cleaned, idxs, func(w *WorkspaceSettings) string { return w.Code }); ok && v != "" {
			fs.Code = v
			any = true
			for _, i := range idxs {
				cleaned[i].Code = ""
			}
		}
		if v, ok := allSameAutoChildren(cleaned, idxs); ok && len(v) > 0 {
			fs.AutoChildren = v
			any = true
			for _, i := range idxs {
				cleaned[i].AutoChildren = nil
			}
		}

		if any {
			folders[wd] = fs
		}
	}
	return cleaned, folders
}

func allSameString(ws []WorkspaceSettings, idxs []int, get func(*WorkspaceSettings) string) (string, bool) {
	if len(idxs) == 0 {
		return "", false
	}
	first := get(&ws[idxs[0]])
	for _, i := range idxs[1:] {
		if get(&ws[i]) != first {
			return "", false
		}
	}
	return first, true
}

func allSameAutoChildren(ws []WorkspaceSettings, idxs []int) ([]AutoChild, bool) {
	if len(idxs) == 0 {
		return nil, false
	}
	first := ws[idxs[0]].AutoChildren
	for _, i := range idxs[1:] {
		if !autoChildrenEqual(first, ws[i].AutoChildren) {
			return nil, false
		}
	}
	if len(first) == 0 {
		return nil, true
	}
	out := make([]AutoChild, len(first))
	copy(out, first)
	return out, true
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
		if av.Name != bv.Name || av.Color != bv.Color || av.Code != bv.Code {
			return false
		}
		if !autoChildrenEqual(av.AutoChildren, bv.AutoChildren) {
			return false
		}
	}
	return true
}
