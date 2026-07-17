// Package workspaces contains the workspace configuration model (WorkspaceSettings,
// AutoChild, WorkspacesFile), folder-level defaults (FolderSettings, ShortcutButton,
// BeadsFolderSettings, FoldersFile) and their loaders/savers, the ACP-server
// constraint matcher used by both workspaces and model profiles, and the
// restricted-runner configuration types. Split out of internal/config to shrink
// that package to core schema only and eliminate its role as the god-package
// (mitto-b8k.3).
package workspaces
