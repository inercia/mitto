// Package-level aliases re-exporting the prompts sub-package's public API from
// internal/config. Kept so existing callers using `config.PromptFile`,
// `config.WebPrompt`, `config.MergePrompts`, etc. continue to compile after the
// extraction (mitto-b8k.3, step 2). New code should import
// github.com/inercia/mitto/internal/prompts directly.
package config

import (
	"github.com/inercia/mitto/internal/prompts"
)

// --- Types (webprompt.go, formerly config.go) ---
type WebPrompt = prompts.WebPrompt
type PromptSource = prompts.PromptSource

// --- Types (prompts.go) ---
type PromptLoop = prompts.PromptLoop
type PromptLoopSchedule = prompts.PromptLoopSchedule
type PromptLoopOnCompletion = prompts.PromptLoopOnCompletion
type PromptLoopOnTasks = prompts.PromptLoopOnTasks
type PromptLoopOnChild = prompts.PromptLoopOnChild
type PromptTarget = prompts.PromptTarget
type PromptParameterCache = prompts.PromptParameterCache
type PromptParameter = prompts.PromptParameter
type PromptPreferredModel = prompts.PromptPreferredModel
type PromptFile = prompts.PromptFile
type PromptLoadError = prompts.PromptLoadError

// --- Types (cache.go) ---
type PromptsCache = prompts.PromptsCache
type PromptsSnapshot = prompts.PromptsSnapshot

// --- Types (watcher.go) ---
type PromptsChangeEvent = prompts.PromptsChangeEvent
type PromptsSubscriber = prompts.PromptsSubscriber
type PromptsWatcher = prompts.PromptsWatcher

// --- Types (migrate.go) ---
type MigratedPrompt = prompts.MigratedPrompt

// --- Types (fragments.go, mitto-g61) ---
type FragmentRegistry = prompts.FragmentRegistry
type FragmentLoadError = prompts.FragmentLoadError

// --- Constants ---
const (
	PromptSourceFile       = prompts.PromptSourceFile
	PromptSourceSettings   = prompts.PromptSourceSettings
	PromptSourceWorkspace  = prompts.PromptSourceWorkspace
	PromptSourceBuiltin    = prompts.PromptSourceBuiltin
	PromptLoopModeAlways   = prompts.PromptLoopModeAlways
	PromptLoopModeOptional = prompts.PromptLoopModeOptional
	DebounceDelay          = prompts.DebounceDelay
)

// --- Vars (KnownPromptParameterTypes, KnownPromptCacheDestinations) ---
var (
	KnownPromptParameterTypes    = prompts.KnownPromptParameterTypes
	KnownPromptCacheDestinations = prompts.KnownPromptCacheDestinations
)

// --- Functions as var-delegates ---
var (
	IsKnownPromptParameterType                = prompts.IsKnownPromptParameterType
	ValidatePromptParameters                  = prompts.ValidatePromptParameters
	DeprecatedMittoVars                       = prompts.DeprecatedMittoVars
	DeprecatedMittoVarReplacement             = prompts.DeprecatedMittoVarReplacement
	WarnDeprecatedMittoVars                   = prompts.WarnDeprecatedMittoVars
	HasTemplateSyntax                         = prompts.HasTemplateSyntax
	PrecompileTemplateConds                   = prompts.PrecompileTemplateConds
	ValidatePromptTemplateSyntax              = prompts.ValidatePromptTemplateSyntax
	RenderPromptTemplate                      = prompts.RenderPromptTemplate
	ValidatePromptLoop                        = prompts.ValidatePromptLoop
	ValidateLoopTriggers                      = prompts.ValidateLoopTriggers
	ValidatePromptTarget                      = prompts.ValidatePromptTarget
	ParsePromptFile                           = prompts.ParsePromptFile
	LoadPromptFile                            = prompts.LoadPromptFile
	LoadPromptsFromDir                        = prompts.LoadPromptsFromDir
	LoadPromptsFromDirWithErrors              = prompts.LoadPromptsFromDirWithErrors
	PromptsToWebPrompts                       = prompts.PromptsToWebPrompts
	FilterPromptsSpecificToACP                = prompts.FilterPromptsSpecificToACP
	GetPromptsDirModTime                      = prompts.GetPromptsDirModTime
	CollectRequiredToolPatterns               = prompts.CollectRequiredToolPatterns
	CollectRequiredToolPatternsFromWebPrompts = prompts.CollectRequiredToolPatternsFromWebPrompts
	UpdatePromptFileEnabled                   = prompts.UpdatePromptFileEnabled
	SlugifyPromptName                         = prompts.SlugifyPromptName
	NewPromptsCache                           = prompts.NewPromptsCache
	NewPromptsWatcher                         = prompts.NewPromptsWatcher
	MigrateMarkdownPromptsInDir               = prompts.MigrateMarkdownPromptsInDir
	MergePrompts                              = prompts.MergePrompts
	MergePromptsKeepDisabled                  = prompts.MergePromptsKeepDisabled
	// --- Fragments (mitto-g61) ---
	NewFragmentRegistry     = prompts.NewFragmentRegistry
	LoadFragmentsFromDir    = prompts.LoadFragmentsFromDir
	ReloadFragmentsFromDirs = prompts.ReloadFragmentsFromDirs
	CurrentFragments        = prompts.CurrentFragments
	SetCurrentFragments     = prompts.SetCurrentFragments
)
