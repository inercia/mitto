package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	celcommon "github.com/google/cel-go/common"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// CompiledExpression holds a compiled, executable CEL program ready for evaluation.
// Programs are cached for performance: all context-dependent functions are bound as
// context-free internal functions (fed via activation variables) so a single program
// can be reused across evaluations.
type CompiledExpression struct {
	expr           string
	prog           cel.Program
	referencesItem bool
}

// String returns the original expression string.
func (c *CompiledExpression) String() string { return c.expr }

// ReferencesItem reports whether the expression references the item.* namespace.
// List endpoints use this to keep single-pass behavior for prompts that don't
// depend on per-row item data, re-evaluating only item-referencing expressions.
func (c *CompiledExpression) ReferencesItem() bool { return c.referencesItem }

// CELEvaluator evaluates CEL expressions against a PromptEnabledContext.
// Compiled ASTs are cached for performance.
type CELEvaluator struct {
	env   *cel.Env
	mu    sync.RWMutex
	cache map[string]*CompiledExpression
}

// NewCELEvaluator creates and returns a new CELEvaluator with all context
// variables registered and the tools.hasPattern custom function available.
func NewCELEvaluator() (*CELEvaluator, error) {
	env, err := cel.NewEnv(
		// ACP variables
		cel.Variable("acp.name", cel.StringType),
		cel.Variable("acp.type", cel.StringType),
		cel.Variable("acp.tags", cel.ListType(cel.StringType)),
		cel.Variable("acp.autoApprove", cel.BoolType),

		// Workspace variables
		cel.Variable("workspace.uuid", cel.StringType),
		cel.Variable("workspace.folder", cel.StringType),
		cel.Variable("workspace.name", cel.StringType),
		cel.Variable("workspace.hasUserDataSchema", cel.BoolType),
		cel.Variable("workspace.hasMittoRC", cel.BoolType),
		cel.Variable("workspace.hasMetadataDescription", cel.BoolType),

		// Session variables
		cel.Variable("session.id", cel.StringType),
		cel.Variable("session.name", cel.StringType),
		cel.Variable("session.isChild", cel.BoolType),
		cel.Variable("session.isAutoChild", cel.BoolType),
		cel.Variable("session.parentId", cel.StringType),
		cel.Variable("session.isPeriodic", cel.BoolType),
		cel.Variable("session.isPeriodicForced", cel.BoolType),
		cel.Variable("session.isPeriodicConversation", cel.BoolType),
		cel.Variable("session.hasBeadsIssue", cel.BoolType),
		cel.Variable("session.beadsIssue", cel.StringType),

		// Parent variables
		cel.Variable("parent.exists", cel.BoolType),
		cel.Variable("parent.name", cel.StringType),
		cel.Variable("parent.acpServer", cel.StringType),

		// Children variables
		cel.Variable("children.count", cel.IntType),
		cel.Variable("children.exists", cel.BoolType),
		cel.Variable("children.mcpCount", cel.IntType),
		cel.Variable("children.mcp_count", cel.IntType), // deprecated alias for children.mcpCount
		cel.Variable("children.names", cel.ListType(cel.StringType)),
		cel.Variable("children.acpServers", cel.ListType(cel.StringType)),
		cel.Variable("children.promptingCount", cel.IntType),
		cel.Variable("children.idleCount", cel.IntType),

		// Tools variables
		cel.Variable("tools.available", cel.BoolType),
		cel.Variable("tools.names", cel.ListType(cel.StringType)),

		// Permissions variables
		cel.Variable("permissions.canDoIntrospection", cel.BoolType),
		cel.Variable("permissions.canSendPrompt", cel.BoolType),
		cel.Variable("permissions.canPromptUser", cel.BoolType),
		cel.Variable("permissions.canStartConversation", cel.BoolType),
		cel.Variable("permissions.canInteractOtherWorkspaces", cel.BoolType),
		cel.Variable("permissions.autoApprovePermissions", cel.BoolType),

		// item.* namespace variable (generic per-row context for list menus).
		// Declared as a map so expressions like item.status compile; values are
		// supplied per-row by callers via the activation. ReferencesItem reports
		// whether a compiled expression touches this namespace.
		cel.Variable("item", cel.MapType(cel.StringType, cel.DynType)),

		// args — prompt arguments supplied at send time (nil/empty at menu time).
		// Declared as map<string,dyn> (same pattern as item) so CEL's native adapter
		// handles map[string]any values correctly. Nil ctx.Args is normalized to an
		// empty map in buildActivation. Use `"KEY" in args && args["KEY"] == "val"`
		// to safely branch — bare `args["KEY"]` throws when the key is absent.
		cel.Variable("args", cel.MapType(cel.StringType, cel.DynType)),

		// commandExists(name) bool — context-free; bound once here.
		// Returns true if the given command name is found in the system PATH.
		cel.Function("commandExists",
			cel.Overload("commandExists_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(commandExistsImpl()),
			),
		),

		// Internal context-free functions. User-facing calls such as
		// tools.hasPattern(p), acp.matchesServerType(t), fileExists(path) are
		// rewritten into these by the macros below, sourcing their context
		// (tool names, ACP identity, workspace folder) from activation variables.
		// Because the bindings are pure functions of their arguments, the compiled
		// cel.Program can be created once at compile time and reused per evaluation.
		cel.Function("__mitto_hasPattern",
			cel.Overload("__mitto_hasPattern_bool_list_string",
				[]*cel.Type{cel.BoolType, cel.ListType(cel.StringType), cel.StringType},
				cel.BoolType,
				cel.FunctionBinding(mittoHasPattern),
			),
		),
		cel.Function("__mitto_hasAllPatterns",
			cel.Overload("__mitto_hasAllPatterns_bool_list_string",
				[]*cel.Type{cel.BoolType, cel.ListType(cel.StringType), cel.StringType},
				cel.BoolType,
				cel.FunctionBinding(mittoHasAllPatterns),
			),
			cel.Overload("__mitto_hasAllPatterns_bool_list_list",
				[]*cel.Type{cel.BoolType, cel.ListType(cel.StringType), cel.ListType(cel.StringType)},
				cel.BoolType,
				cel.FunctionBinding(mittoHasAllPatterns),
			),
		),
		cel.Function("__mitto_hasAnyPattern",
			cel.Overload("__mitto_hasAnyPattern_bool_list_string",
				[]*cel.Type{cel.BoolType, cel.ListType(cel.StringType), cel.StringType},
				cel.BoolType,
				cel.FunctionBinding(mittoHasAnyPattern),
			),
			cel.Overload("__mitto_hasAnyPattern_bool_list_list",
				[]*cel.Type{cel.BoolType, cel.ListType(cel.StringType), cel.ListType(cel.StringType)},
				cel.BoolType,
				cel.FunctionBinding(mittoHasAnyPattern),
			),
		),
		cel.Function("__mitto_matchesServerType",
			cel.Overload("__mitto_matchesServerType_string_string_string",
				[]*cel.Type{cel.StringType, cel.StringType, cel.StringType},
				cel.BoolType,
				cel.FunctionBinding(mittoMatchesServerType),
			),
			cel.Overload("__mitto_matchesServerType_string_string_list",
				[]*cel.Type{cel.StringType, cel.StringType, cel.ListType(cel.StringType)},
				cel.BoolType,
				cel.FunctionBinding(mittoMatchesServerType),
			),
		),
		cel.Function("__mitto_fileExists",
			cel.Overload("__mitto_fileExists_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				cel.BoolType,
				cel.BinaryBinding(mittoFileExists),
			),
		),
		cel.Function("__mitto_dirExists",
			cel.Overload("__mitto_dirExists_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				cel.BoolType,
				cel.BinaryBinding(mittoDirExists),
			),
		),

		// Macros rewrite user-facing convenience calls into the internal
		// context-free functions above, injecting activation-sourced arguments.
		// They run at parse time (before type-checking), so the original
		// tools.*/acp.*/fileExists/dirExists calls never reach the checker.
		cel.Macros(
			cel.ReceiverMacro("hasPattern", 1, toolsHasPatternMacro),
			cel.ReceiverMacro("hasAllPatterns", 1, toolsHasAllPatternsMacro),
			cel.ReceiverMacro("hasAnyPattern", 1, toolsHasAnyPatternMacro),
			cel.ReceiverMacro("matchesServerType", 1, acpMatchesServerTypeMacro),
			cel.GlobalMacro("fileExists", 1, fileExistsMacro),
			cel.GlobalMacro("dirExists", 1, dirExistsMacro),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("cel: failed to create environment: %w", err)
	}

	return &CELEvaluator{
		env:   env,
		cache: make(map[string]*CompiledExpression),
	}, nil
}

// Compile validates and compiles a CEL expression, caching the AST.
// Returns an error if the expression has a syntax or type error.
func (e *CELEvaluator) Compile(expression string) (*CompiledExpression, error) {
	e.mu.RLock()
	if ce, ok := e.cache[expression]; ok {
		e.mu.RUnlock()
		return ce, nil
	}
	e.mu.RUnlock()

	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel: compile error in %q: %w", expression, issues.Err())
	}

	// Build the executable program once. Because all context-dependent functions
	// are now context-free (fed from activation variables via the parse-time
	// macros), the program is fully reusable across evaluations and is cached.
	prog, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: program error in %q: %w", expression, err)
	}

	ce := &CompiledExpression{
		expr:           expression,
		prog:           prog,
		referencesItem: referencesItemNamespace(ast),
	}

	e.mu.Lock()
	e.cache[expression] = ce
	e.mu.Unlock()

	return ce, nil
}

// referencesItemNamespace reports whether the AST references the item.* namespace
// (the bare "item" identifier or any "item."-prefixed qualified name).
func referencesItemNamespace(ast *cel.Ast) bool {
	matches := celast.MatchDescendants(
		celast.NavigateAST(ast.NativeRep()),
		func(e celast.NavigableExpr) bool {
			if e.Kind() != celast.IdentKind {
				return false
			}
			name := e.AsIdent()
			return name == "item" || strings.HasPrefix(name, "item.")
		},
	)
	return len(matches) > 0
}

// Evaluate runs a compiled CEL expression against the provided context.
// Returns true if the expression evaluates to true (visible), false otherwise.
// If ctx is nil, defaults to true (visible).
func (e *CELEvaluator) Evaluate(compiled *CompiledExpression, ctx *PromptEnabledContext) (bool, error) {
	if ctx == nil {
		return true, nil
	}

	out, _, err := compiled.prog.Eval(buildActivation(ctx))
	if err != nil {
		return true, fmt.Errorf("cel: evaluation error for %q: %w", compiled.expr, err)
	}

	result, ok := out.(types.Bool)
	if !ok {
		return true, fmt.Errorf("cel: expression %q did not return a bool (got %T)", compiled.expr, out)
	}

	return bool(result), nil
}

// buildActivation converts a PromptEnabledContext into a CEL activation map.
func buildActivation(ctx *PromptEnabledContext) map[string]any {
	// Convert Args to map[string]any (matching the args variable's DynType declaration)
	// so CEL's native adapter can handle subscript access correctly. Nil args is
	// normalized to an empty map so `"KEY" in args` never panics.
	argsAny := make(map[string]any, len(ctx.Args))
	for k, v := range ctx.Args {
		argsAny[k] = v
	}
	return map[string]any{
		"acp.name":        ctx.ACP.Name,
		"acp.type":        ctx.ACP.Type,
		"acp.tags":        ctx.ACP.Tags,
		"acp.autoApprove": ctx.ACP.AutoApprove,

		"workspace.uuid":                   ctx.Workspace.UUID,
		"workspace.folder":                 ctx.Workspace.Folder,
		"workspace.name":                   ctx.Workspace.Name,
		"workspace.hasUserDataSchema":      ctx.Workspace.HasUserDataSchema,
		"workspace.hasMittoRC":             ctx.Workspace.HasMittoRC,
		"workspace.hasMetadataDescription": ctx.Workspace.HasMetadataDescription,

		"session.id":                     ctx.Session.ID,
		"session.name":                   ctx.Session.Name,
		"session.isChild":                ctx.Session.IsChild,
		"session.isAutoChild":            ctx.Session.IsAutoChild,
		"session.parentId":               ctx.Session.ParentID,
		"session.isPeriodic":             ctx.Session.IsPeriodic,
		"session.isPeriodicForced":       ctx.Session.IsPeriodicForced,
		"session.isPeriodicConversation": ctx.Session.IsPeriodicConversation,
		"session.hasBeadsIssue":          ctx.Session.HasBeadsIssue,
		"session.beadsIssue":             ctx.Session.BeadsIssue,

		"parent.exists":    ctx.Parent.Exists,
		"parent.name":      ctx.Parent.Name,
		"parent.acpServer": ctx.Parent.ACPServer,

		"children.count":          int64(ctx.Children.Count),
		"children.exists":         ctx.Children.Exists,
		"children.mcpCount":       int64(ctx.Children.MCPCount),
		"children.mcp_count":      int64(ctx.Children.MCPCount), // deprecated alias
		"children.names":          ctx.Children.Names,
		"children.acpServers":     ctx.Children.ACPServers,
		"children.promptingCount": int64(ctx.Children.PromptingCount),
		"children.idleCount":      int64(ctx.Children.IdleCount),

		"tools.available": ctx.Tools.Available,
		"tools.names":     ctx.Tools.Names,

		"permissions.canDoIntrospection":         ctx.Permissions.CanDoIntrospection,
		"permissions.canSendPrompt":              ctx.Permissions.CanSendPrompt,
		"permissions.canPromptUser":              ctx.Permissions.CanPromptUser,
		"permissions.canStartConversation":       ctx.Permissions.CanStartConversation,
		"permissions.canInteractOtherWorkspaces": ctx.Permissions.CanInteractOtherWorkspaces,
		"permissions.autoApprovePermissions":     ctx.Permissions.AutoApprovePermissions,

		// item.* per-row context. All keys are always present (empty string when
		// no item context is set) so expressions like item.status resolve cleanly.
		// Callers populate ctx.Item for per-row list-menu evaluation (mitto-o0u.1).
		// See ReferencesItem for how callers detect item-dependent expressions.
		"item": map[string]any{
			"id":       ctx.Item.Id,
			"status":   ctx.Item.Status,
			"type":     ctx.Item.Type,
			"priority": ctx.Item.Priority,
			"kind":     ctx.Item.Kind,
		},

		// args — prompt arguments. Empty at menu time; populated at send time.
		"args": argsAny,
	}
}

// Global CEL evaluator singleton
var (
	globalCELEvaluator     *CELEvaluator
	globalCELEvaluatorOnce sync.Once
)

// GetCELEvaluator returns the global CEL evaluator singleton.
// Returns nil if initialization failed (logs error internally).
// Thread-safe; creates the evaluator on first call.
func GetCELEvaluator() *CELEvaluator {
	globalCELEvaluatorOnce.Do(func() {
		globalCELEvaluator, _ = NewCELEvaluator()
	})
	return globalCELEvaluator
}

// isIdent reports whether e is a bare identifier with the given name.
func isIdent(e celast.Expr, name string) bool {
	return e != nil && e.Kind() == celast.IdentKind && e.AsIdent() == name
}

// toolsHasPatternMacro rewrites tools.hasPattern(p) -> __mitto_hasPattern(tools.available, tools.names, p).
func toolsHasPatternMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasPattern", eh.NewIdent("tools.available"), eh.NewIdent("tools.names"), args[0]), nil
}

// toolsHasAllPatternsMacro rewrites tools.hasAllPatterns(a) -> __mitto_hasAllPatterns(tools.available, tools.names, a).
func toolsHasAllPatternsMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasAllPatterns", eh.NewIdent("tools.available"), eh.NewIdent("tools.names"), args[0]), nil
}

// toolsHasAnyPatternMacro rewrites tools.hasAnyPattern(a) -> __mitto_hasAnyPattern(tools.available, tools.names, a).
func toolsHasAnyPatternMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasAnyPattern", eh.NewIdent("tools.available"), eh.NewIdent("tools.names"), args[0]), nil
}

// acpMatchesServerTypeMacro rewrites acp.matchesServerType(t) ->
// __mitto_matchesServerType(acp.name, acp.type, t).
func acpMatchesServerTypeMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "acp") {
		return nil, nil
	}
	return eh.NewCall("__mitto_matchesServerType", eh.NewIdent("acp.name"), eh.NewIdent("acp.type"), args[0]), nil
}

// fileExistsMacro rewrites fileExists(p) -> __mitto_fileExists(workspace.folder, p).
func fileExistsMacro(eh cel.MacroExprFactory, _ celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	return eh.NewCall("__mitto_fileExists", eh.NewIdent("workspace.folder"), args[0]), nil
}

// dirExistsMacro rewrites dirExists(p) -> __mitto_dirExists(workspace.folder, p).
func dirExistsMacro(eh cel.MacroExprFactory, _ celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	return eh.NewCall("__mitto_dirExists", eh.NewIdent("workspace.folder"), args[0]), nil
}

// valToString returns the Go string for a CEL string value, or "" otherwise.
func valToString(v ref.Val) string {
	if s, ok := v.(types.String); ok {
		return string(s)
	}
	return ""
}

// mittoHasPattern reports whether any name (args[1], a list) matches the glob
// pattern (args[2]). args[0] is tools.available. Context-free so the compiled
// program can be cached. Delegates to hasPattern (templatefuncs.go) for the
// pure-Go logic (single source of truth shared with the template FuncMap).
func mittoHasPattern(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.Bool(false)
	}
	available, ok := args[0].(types.Bool)
	if !ok {
		return types.Bool(true) // type error → treat as unavailable → fail-open
	}
	pattern, ok := args[2].(types.String)
	if !ok {
		return types.Bool(false)
	}
	names := extractStringArgs([]ref.Val{args[1]})
	return types.Bool(hasPattern(bool(available), names, string(pattern)))
}

// mittoHasAllPatterns reports whether ALL patterns (args[2], string or list)
// are satisfied by at least one name each (args[1], a list). args[0] is
// tools.available. Delegates to hasAllPatterns (templatefuncs.go).
func mittoHasAllPatterns(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.Bool(false)
	}
	available, ok := args[0].(types.Bool)
	if !ok {
		return types.Bool(true) // type error → fail-open
	}
	names := extractStringArgs([]ref.Val{args[1]})
	patterns := extractStringArgs([]ref.Val{args[2]})
	return types.Bool(hasAllPatterns(bool(available), names, patterns))
}

// mittoHasAnyPattern reports whether ANY pattern (args[2], string or list)
// is satisfied by at least one name (args[1], a list). args[0] is
// tools.available. Delegates to hasAnyPattern (templatefuncs.go).
func mittoHasAnyPattern(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.Bool(false)
	}
	available, ok := args[0].(types.Bool)
	if !ok {
		return types.Bool(true) // type error → fail-open
	}
	names := extractStringArgs([]ref.Val{args[1]})
	patterns := extractStringArgs([]ref.Val{args[2]})
	return types.Bool(hasAnyPattern(bool(available), names, patterns))
}

// mittoMatchesServerType reports whether the ACP server type matches any of the
// given types (case-insensitive). args[0]=acp.name, args[1]=acp.type, args[2:]=types.
// Only compares the server type (e.g., "augment"), not the display name.
// Delegates to matchesServerType (templatefuncs.go).
func mittoMatchesServerType(args ...ref.Val) ref.Val {
	if len(args) < 2 {
		return types.Bool(false)
	}
	acpName := valToString(args[0])
	acpType := valToString(args[1])
	serverTypes := extractStringArgs(args[2:])
	return types.Bool(matchesServerType(acpName, acpType, serverTypes))
}

// commandExistsImpl returns a CEL UnaryOp that checks whether a command
// is available in the system PATH. Delegates to commandExists (templatefuncs.go).
func commandExistsImpl() func(ref.Val) ref.Val {
	return func(nameVal ref.Val) ref.Val {
		name, ok := nameVal.(types.String)
		if !ok {
			return types.Bool(false)
		}
		return types.Bool(commandExists(string(name)))
	}
}

// statResolved stats path, resolving relative paths against workspaceFolder.
// Returns (info, true) on success, or (nil, false) for empty paths or stat errors.
func statResolved(workspaceFolder, path string) (os.FileInfo, bool) {
	if path == "" {
		return nil, false
	}
	if !filepath.IsAbs(path) && workspaceFolder != "" {
		path = filepath.Join(workspaceFolder, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	return info, true
}

// mittoFileExists reports whether path exists and is a regular file (not a dir).
// Relative paths are resolved against the workspace folder (first argument).
// Delegates to fileExists (templatefuncs.go).
func mittoFileExists(folderVal, pathVal ref.Val) ref.Val {
	return types.Bool(fileExists(valToString(folderVal), valToString(pathVal)))
}

// mittoDirExists reports whether path exists and is a directory.
// Relative paths are resolved against the workspace folder (first argument).
// Delegates to dirExists (templatefuncs.go).
func mittoDirExists(folderVal, pathVal ref.Val) ref.Val {
	return types.Bool(dirExists(valToString(folderVal), valToString(pathVal)))
}

// extractStringArgs extracts string values from CEL function arguments.
// Handles both individual string args and list(string) args.
func extractStringArgs(args []ref.Val) []string {
	var result []string
	for _, arg := range args {
		switch v := arg.(type) {
		case types.String:
			result = append(result, string(v))
		case traits.Lister:
			size := v.Size().(types.Int)
			for i := types.IntZero; i < size; i++ {
				if s, ok := v.Get(i).(types.String); ok {
					result = append(result, string(s))
				}
			}
		}
	}
	return result
}
