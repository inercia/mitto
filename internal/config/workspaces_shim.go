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
type ModelOverride = workspaces.ModelOverride
type ModelOverrides = workspaces.ModelOverrides
type WorkspaceSettings = workspaces.WorkspaceSettings

// --- Types (folders.go) ---
type ShortcutButton = workspaces.ShortcutButton
type TaskLabelColor = workspaces.TaskLabelColor
type FolderSettings = workspaces.FolderSettings
type BeadsFolderSettings = workspaces.BeadsFolderSettings
type BeadsDatabaseMode = workspaces.BeadsDatabaseMode
type FoldersFile = workspaces.FoldersFile

// --- Types (constraint.go) ---
type ACPServerConstraint = workspaces.ACPServerConstraint

// --- Types (runner_config.go) ---
type RunnerRestrictions = workspaces.RunnerRestrictions
type DockerRestrictions = workspaces.DockerRestrictions
type WorkspaceRunnerConfig = workspaces.WorkspaceRunnerConfig

// --- Types (permissions.go) ---
type PermissionMode = workspaces.PermissionMode
type PermissionSettings = workspaces.PermissionSettings
type SystemOneConfig = workspaces.SystemOneConfig
type SystemOneFallback = workspaces.SystemOneFallback

// --- Constants ---
const (
	MaxAutoChildren         = workspaces.MaxAutoChildren
	BeadsDatabaseModeLocal  = workspaces.BeadsDatabaseModeLocal
	BeadsDatabaseModeShared = workspaces.BeadsDatabaseModeShared

	PermissionModeUnset     = workspaces.PermissionModeUnset
	PermissionModeAsk       = workspaces.PermissionModeAsk
	PermissionModeAllow     = workspaces.PermissionModeAllow
	PermissionModeSystemOne = workspaces.PermissionModeSystemOne

	SystemOneFallbackAsk    = workspaces.SystemOneFallbackAsk
	SystemOneFallbackReject = workspaces.SystemOneFallbackReject
)

// --- Vars ---
var ValidRunnerTypes = workspaces.ValidRunnerTypes
var ValidPermissionModes = workspaces.ValidPermissionModes
var ValidSystemOneFallbacks = workspaces.ValidSystemOneFallbacks

// --- Functions as var-delegates ---
var (
	NormalizeDefaultWorkspaces = workspaces.NormalizeDefaultWorkspaces
	LoadWorkspaces             = workspaces.LoadWorkspaces
	LoadWorkspacesFromFile     = workspaces.LoadWorkspacesFromFile
	SaveWorkspaces             = workspaces.SaveWorkspaces

	IsValidPermissionMode      = workspaces.IsValidPermissionMode
	DefaultSystemOneConfig     = workspaces.DefaultSystemOneConfig
	ValidateSystemOneConfig    = workspaces.ValidateSystemOneConfig
	ValidatePermissionSettings = workspaces.ValidatePermissionSettings
	MigrateLegacyAutoApprove   = workspaces.MigrateLegacyAutoApprove

	LoadFolders                       = workspaces.LoadFolders
	LoadFoldersFromFile               = workspaces.LoadFoldersFromFile
	SaveFolders                       = workspaces.SaveFolders
	ApplyFolderDefaults               = workspaces.ApplyFolderDefaults
	SetFolderBeadsUpstream            = workspaces.SetFolderBeadsUpstream
	SetFolderBeadsPromptUpstream      = workspaces.SetFolderBeadsPromptUpstream
	SetFolderBeadsDatabaseMode        = workspaces.SetFolderBeadsDatabaseMode
	ConfiguredFolderBeadsDatabaseMode = workspaces.ConfiguredFolderBeadsDatabaseMode
	IsValidBeadsDatabaseMode          = workspaces.IsValidBeadsDatabaseMode
	FolderBeadsUpstream               = workspaces.FolderBeadsUpstream
	FolderBeadsPrompts                = workspaces.FolderBeadsPrompts
	FolderBeadsPromptArgs             = workspaces.FolderBeadsPromptArgs
	FolderShortcuts                   = workspaces.FolderShortcuts
	SetFolderShortcuts                = workspaces.SetFolderShortcuts
	FolderTaskLabelColors             = workspaces.FolderTaskLabelColors
	SetFolderTaskLabelColors          = workspaces.SetFolderTaskLabelColors
	SetFolderPinned                   = workspaces.SetFolderPinned
	FolderPinned                      = workspaces.FolderPinned
	SetFolderLastOpenedAt             = workspaces.SetFolderLastOpenedAt
	FolderLastOpenedAt                = workspaces.FolderLastOpenedAt

	ConstraintMatchesName = workspaces.ConstraintMatchesName
)
