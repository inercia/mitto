// Package-level aliases re-exporting the workspaces sub-package's public API
// from internal/config. Kept so existing callers using
// `config.WorkspaceSettings`, `config.LoadWorkspaces`, `config.FolderSettings`,
// `config.ApplyFolderDefaults`, `config.ACPServerConstraint`,
// `config.WorkspaceRunnerConfig`, etc. continue to compile after the extraction
// (mitto-b8k.3, step 4). New code should import
// github.com/inercia/mitto/internal/workspaces directly.
package config

import (
	"github.com/inercia/mitto/internal/workspaces"
)

// --- Types (workspaces.go) ---
type WorkspacesFile = workspaces.WorkspacesFile
type AutoChild = workspaces.AutoChild
type WorkspaceSettings = workspaces.WorkspaceSettings

// --- Types (folders.go) ---
type ShortcutButton = workspaces.ShortcutButton
type FolderSettings = workspaces.FolderSettings
type BeadsFolderSettings = workspaces.BeadsFolderSettings
type FoldersFile = workspaces.FoldersFile

// --- Types (constraint.go) ---
type ACPServerConstraint = workspaces.ACPServerConstraint

// --- Types (runner_config.go) ---
type RunnerRestrictions = workspaces.RunnerRestrictions
type DockerRestrictions = workspaces.DockerRestrictions
type WorkspaceRunnerConfig = workspaces.WorkspaceRunnerConfig

// --- Constants ---
const MaxAutoChildren = workspaces.MaxAutoChildren

// --- Vars ---
var ValidRunnerTypes = workspaces.ValidRunnerTypes

// --- Functions as var-delegates ---
var (
	NormalizeDefaultWorkspaces = workspaces.NormalizeDefaultWorkspaces
	LoadWorkspaces             = workspaces.LoadWorkspaces
	LoadWorkspacesFromFile     = workspaces.LoadWorkspacesFromFile
	SaveWorkspaces             = workspaces.SaveWorkspaces

	LoadFolders                  = workspaces.LoadFolders
	LoadFoldersFromFile          = workspaces.LoadFoldersFromFile
	SaveFolders                  = workspaces.SaveFolders
	ApplyFolderDefaults          = workspaces.ApplyFolderDefaults
	SetFolderBeadsUpstream       = workspaces.SetFolderBeadsUpstream
	SetFolderBeadsPromptUpstream = workspaces.SetFolderBeadsPromptUpstream
	FolderBeadsUpstream          = workspaces.FolderBeadsUpstream
	FolderBeadsPrompts           = workspaces.FolderBeadsPrompts
	FolderBeadsPromptArgs        = workspaces.FolderBeadsPromptArgs
	FolderShortcuts              = workspaces.FolderShortcuts
	SetFolderShortcuts           = workspaces.SetFolderShortcuts
	SetFolderPinned              = workspaces.SetFolderPinned
	FolderPinned                 = workspaces.FolderPinned

	ConstraintMatchesName = workspaces.ConstraintMatchesName
)
