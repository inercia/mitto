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
		cel.Variable("ACP.Name", cel.StringType),
		cel.Variable("ACP.Type", cel.StringType),
		cel.Variable("ACP.Tags", cel.ListType(cel.StringType)),
		cel.Variable("ACP.AutoApprove", cel.BoolType),

		// Workspace variables
		cel.Variable("Workspace.UUID", cel.StringType),
		cel.Variable("Workspace.Folder", cel.StringType),
		cel.Variable("Workspace.Name", cel.StringType),
		cel.Variable("Workspace.HasUserDataSchema", cel.BoolType),
		cel.Variable("Workspace.HasMittoRC", cel.BoolType),
		cel.Variable("Workspace.HasMetadataDescription", cel.BoolType),

		// Session variables
		cel.Variable("Session.ID", cel.StringType),
		cel.Variable("Session.Name", cel.StringType),
		cel.Variable("Session.IsChild", cel.BoolType),
		cel.Variable("Session.IsAutoChild", cel.BoolType),
		cel.Variable("Session.ParentID", cel.StringType),
		cel.Variable("Session.IsPeriodic", cel.BoolType),
		cel.Variable("Session.IsPeriodicForced", cel.BoolType),
		cel.Variable("Session.IsPeriodicConversation", cel.BoolType),
		cel.Variable("Session.HasBeadsIssue", cel.BoolType),
		cel.Variable("Session.BeadsIssue", cel.StringType),
		cel.Variable("Session.ModelTags", cel.ListType(cel.StringType)),

		// Parent variables
		cel.Variable("Parent.Exists", cel.BoolType),
		cel.Variable("Parent.Name", cel.StringType),
		cel.Variable("Parent.ACPServer", cel.StringType),

		// Children variables
		cel.Variable("Children.Count", cel.IntType),
		cel.Variable("Children.Exists", cel.BoolType),
		cel.Variable("Children.MCPCount", cel.IntType),
		cel.Variable("Children.Names", cel.ListType(cel.StringType)),
		cel.Variable("Children.ACPServers", cel.ListType(cel.StringType)),
		cel.Variable("Children.PromptingCount", cel.IntType),
		cel.Variable("Children.IdleCount", cel.IntType),

		// Tools variables
		cel.Variable("Tools.Available", cel.BoolType),
		cel.Variable("Tools.Names", cel.ListType(cel.StringType)),

		// Permissions variables
		cel.Variable("Permissions.CanDoIntrospection", cel.BoolType),
		cel.Variable("Permissions.CanSendPrompt", cel.BoolType),
		cel.Variable("Permissions.CanPromptUser", cel.BoolType),
		cel.Variable("Permissions.CanStartConversation", cel.BoolType),
		cel.Variable("Permissions.CanInteractOtherWorkspaces", cel.BoolType),
		cel.Variable("Permissions.AutoApprovePermissions", cel.BoolType),

		// Item namespace variable (generic per-row context for list menus).
		// Declared as a map so expressions like Item.Status compile; values are
		// supplied per-row by callers via the activation. ReferencesItem reports
		// whether a compiled expression touches this namespace.
		cel.Variable("Item", cel.MapType(cel.StringType, cel.DynType)),

		// Args — prompt arguments supplied at send time (nil/empty at menu time).
		// Declared as map<string,dyn> (same pattern as Item) so CEL's native adapter
		// handles map[string]any values correctly. Nil ctx.Args is normalized to an
		// empty map in buildActivation. Use `"KEY" in Args && Args["KEY"] == "val"`
		// to safely branch — bare `Args["KEY"]` throws when the key is absent.
		cel.Variable("Args", cel.MapType(cel.StringType, cel.DynType)),

		// UserData — per-conversation user data (name→value). Nil at menu time;
		// normalized to empty map in buildActivation so `"KEY" in UserData` is safe.
		cel.Variable("UserData", cel.MapType(cel.StringType, cel.DynType)),

		// CommandExists(name) bool — context-free; bound once here.
		// Returns true if the given command name is found in the system PATH.
		cel.Function("CommandExists",
			cel.Overload("CommandExists_string",
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
		cel.Function("__mitto_hasModelTag",
			cel.Overload("__mitto_hasModelTag_list_string",
				[]*cel.Type{cel.ListType(cel.StringType), cel.StringType},
				cel.BoolType,
				cel.FunctionBinding(mittoHasModelTag),
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
			cel.ReceiverMacro("HasPattern", 1, toolsHasPatternMacro),
			cel.ReceiverMacro("HasModelTag", 1, sessionHasModelTagMacro),
			cel.ReceiverMacro("HasAllPatterns", 1, toolsHasAllPatternsMacro),
			cel.ReceiverMacro("HasAnyPattern", 1, toolsHasAnyPatternMacro),
			cel.ReceiverMacro("MatchesServerType", 1, acpMatchesServerTypeMacro),
			cel.GlobalMacro("FileExists", 1, fileExistsMacro),
			cel.GlobalMacro("DirExists", 1, dirExistsMacro),
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

// referencesItemNamespace reports whether the AST references the Item namespace
// (the bare "Item" identifier or any "Item."-prefixed qualified name).
func referencesItemNamespace(ast *cel.Ast) bool {
	matches := celast.MatchDescendants(
		celast.NavigateAST(ast.NativeRep()),
		func(e celast.NavigableExpr) bool {
			if e.Kind() != celast.IdentKind {
				return false
			}
			name := e.AsIdent()
			return name == "Item" || strings.HasPrefix(name, "Item.")
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
	// Convert UserData to map[string]any; normalized to empty so `"KEY" in UserData` is safe.
	userDataAny := make(map[string]any, len(ctx.UserData))
	for k, v := range ctx.UserData {
		userDataAny[k] = v
	}
	return map[string]any{
		"ACP.Name":        ctx.ACP.Name,
		"ACP.Type":        ctx.ACP.Type,
		"ACP.Tags":        ctx.ACP.Tags,
		"ACP.AutoApprove": ctx.ACP.AutoApprove,

		"Workspace.UUID":                   ctx.Workspace.UUID,
		"Workspace.Folder":                 ctx.Workspace.Folder,
		"Workspace.Name":                   ctx.Workspace.Name,
		"Workspace.HasUserDataSchema":      ctx.Workspace.HasUserDataSchema,
		"Workspace.HasMittoRC":             ctx.Workspace.HasMittoRC,
		"Workspace.HasMetadataDescription": ctx.Workspace.HasMetadataDescription,

		"Session.ID":                     ctx.Session.ID,
		"Session.Name":                   ctx.Session.Name,
		"Session.IsChild":                ctx.Session.IsChild,
		"Session.IsAutoChild":            ctx.Session.IsAutoChild,
		"Session.ParentID":               ctx.Session.ParentID,
		"Session.IsPeriodic":             ctx.Session.IsPeriodic,
		"Session.IsPeriodicForced":       ctx.Session.IsPeriodicForced,
		"Session.IsPeriodicConversation": ctx.Session.IsPeriodicConversation,
		"Session.HasBeadsIssue":          ctx.Session.HasBeadsIssue,
		"Session.BeadsIssue":             ctx.Session.BeadsIssue,
		"Session.ModelTags":              ctx.Session.ModelTags,

		"Parent.Exists":    ctx.Parent.Exists,
		"Parent.Name":      ctx.Parent.Name,
		"Parent.ACPServer": ctx.Parent.ACPServer,

		"Children.Count":          int64(ctx.Children.Count),
		"Children.Exists":         ctx.Children.Exists,
		"Children.MCPCount":       int64(ctx.Children.MCPCount),
		"Children.Names":          ctx.Children.Names,
		"Children.ACPServers":     ctx.Children.ACPServers,
		"Children.PromptingCount": int64(ctx.Children.PromptingCount),
		"Children.IdleCount":      int64(ctx.Children.IdleCount),

		"Tools.Available": ctx.Tools.Available,
		"Tools.Names":     ctx.Tools.Names,

		"Permissions.CanDoIntrospection":         ctx.Permissions.CanDoIntrospection,
		"Permissions.CanSendPrompt":              ctx.Permissions.CanSendPrompt,
		"Permissions.CanPromptUser":              ctx.Permissions.CanPromptUser,
		"Permissions.CanStartConversation":       ctx.Permissions.CanStartConversation,
		"Permissions.CanInteractOtherWorkspaces": ctx.Permissions.CanInteractOtherWorkspaces,
		"Permissions.AutoApprovePermissions":     ctx.Permissions.AutoApprovePermissions,

		// Item per-row context. All keys are always present (empty string when
		// no item context is set) so expressions like Item["Status"] resolve cleanly.
		// Callers populate ctx.Item for per-row list-menu evaluation (mitto-o0u.1).
		// See ReferencesItem for how callers detect item-dependent expressions.
		"Item": map[string]any{
			"Id":       ctx.Item.Id,
			"Status":   ctx.Item.Status,
			"Type":     ctx.Item.Type,
			"Priority": ctx.Item.Priority,
			"Kind":     ctx.Item.Kind,
		},

		// Args — prompt arguments. Empty at menu time; populated at send time.
		"Args": argsAny,

		// UserData — per-conversation user data. Empty at menu time; populated at send time.
		"UserData": userDataAny,
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

// toolsHasPatternMacro rewrites Tools.HasPattern(p) -> __mitto_hasPattern(Tools.Available, Tools.Names, p).
func toolsHasPatternMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "Tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasPattern", eh.NewIdent("Tools.Available"), eh.NewIdent("Tools.Names"), args[0]), nil
}

// toolsHasAllPatternsMacro rewrites Tools.HasAllPatterns(a) -> __mitto_hasAllPatterns(Tools.Available, Tools.Names, a).
func toolsHasAllPatternsMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "Tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasAllPatterns", eh.NewIdent("Tools.Available"), eh.NewIdent("Tools.Names"), args[0]), nil
}

// toolsHasAnyPatternMacro rewrites Tools.HasAnyPattern(a) -> __mitto_hasAnyPattern(Tools.Available, Tools.Names, a).
func toolsHasAnyPatternMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "Tools") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasAnyPattern", eh.NewIdent("Tools.Available"), eh.NewIdent("Tools.Names"), args[0]), nil
}

// sessionHasModelTagMacro rewrites Session.HasModelTag(t) -> __mitto_hasModelTag(Session.ModelTags, t).
func sessionHasModelTagMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "Session") {
		return nil, nil
	}
	return eh.NewCall("__mitto_hasModelTag", eh.NewIdent("Session.ModelTags"), args[0]), nil
}

// acpMatchesServerTypeMacro rewrites ACP.MatchesServerType(t) ->
// __mitto_matchesServerType(ACP.Name, ACP.Type, t).
func acpMatchesServerTypeMacro(eh cel.MacroExprFactory, target celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	if !isIdent(target, "ACP") {
		return nil, nil
	}
	return eh.NewCall("__mitto_matchesServerType", eh.NewIdent("ACP.Name"), eh.NewIdent("ACP.Type"), args[0]), nil
}

// fileExistsMacro rewrites FileExists(p) -> __mitto_fileExists(Workspace.Folder, p).
func fileExistsMacro(eh cel.MacroExprFactory, _ celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	return eh.NewCall("__mitto_fileExists", eh.NewIdent("Workspace.Folder"), args[0]), nil
}

// dirExistsMacro rewrites DirExists(p) -> __mitto_dirExists(Workspace.Folder, p).
func dirExistsMacro(eh cel.MacroExprFactory, _ celast.Expr, args []celast.Expr) (celast.Expr, *celcommon.Error) {
	return eh.NewCall("__mitto_dirExists", eh.NewIdent("Workspace.Folder"), args[0]), nil
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

// mittoHasModelTag reports whether tag (args[1]) is present in the model tag list
// (args[0]). Context-free so the compiled program can be cached. Delegates to hasModelTag
// (templatefuncs.go) — single source of truth shared with the Model(tag) template func.
func mittoHasModelTag(args ...ref.Val) ref.Val {
	if len(args) != 2 {
		return types.Bool(false)
	}
	tags := extractStringArgs([]ref.Val{args[0]})
	tag := valToString(args[1])
	return types.Bool(hasModelTag(tags, tag))
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
