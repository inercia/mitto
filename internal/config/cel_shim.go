// Package-level aliases re-exporting the CEL sub-package's public API from
// internal/config. Kept so existing callers using `config.CELEvaluator`,
// `config.PromptEnabledContext`, etc. continue to compile after the extraction
// (mitto-b8k.3). New code should import github.com/inercia/mitto/internal/cel
// directly.
package config

import (
	"github.com/inercia/mitto/internal/cel"
)

// --- Types (cel_evaluator.go) ---
type CompiledExpression = cel.CompiledExpression
type CELEvaluator = cel.CELEvaluator

// --- Types (cel_context.go) ---
type PromptEnabledContext = cel.PromptEnabledContext
type IterationContext = cel.IterationContext
type TriggerContext = cel.TriggerContext
type TriggerOnTasksContext = cel.TriggerOnTasksContext
type TasksChangesView = cel.TasksChangesView
type ACPServerInfo = cel.ACPServerInfo
type ACPContext = cel.ACPContext
type WorkspaceContext = cel.WorkspaceContext
type SessionContext = cel.SessionContext
type ParentContext = cel.ParentContext
type ChildInfo = cel.ChildInfo
type ChildrenContext = cel.ChildrenContext
type ServerToolState = cel.ServerToolState
type ServerToolInfo = cel.ServerToolInfo
type ToolsContext = cel.ToolsContext
type ItemContext = cel.ItemContext
type PermissionsContext = cel.PermissionsContext

// --- Types (tasks_condition.go) ---
type TasksSnapshot = cel.TasksSnapshot
type TasksDelta = cel.TasksDelta
type TasksChangeContext = cel.TasksChangeContext
type TasksConditionEvaluator = cel.TasksConditionEvaluator

// --- Constants ---
const AllServersToolKey = cel.AllServersToolKey

// --- Constructors / package-level functions ---
// Wrapped as `var` delegates so they resolve to the sub-package implementation
// without changing call-site syntax. Cheaper than func wrappers and equivalent
// for identifier resolution.
var (
	NewCELEvaluator            = cel.NewCELEvaluator
	GetCELEvaluator            = cel.GetCELEvaluator
	GroupToolNamesByServer     = cel.GroupToolNamesByServer
	NewReachableToolsContext   = cel.NewReachableToolsContext
	NewProcessorToolsContext   = cel.NewProcessorToolsContext
	ParseTasksSnapshot         = cel.ParseTasksSnapshot
	DiffTasks                  = cel.DiffTasks
	NewTasksConditionEvaluator = cel.NewTasksConditionEvaluator
	ValidateCondition          = cel.ValidateCondition
	FormatACPServers           = cel.FormatACPServers
	FormatChildren             = cel.FormatChildren
	BuildTemplateFuncMap       = cel.BuildTemplateFuncMap
)
