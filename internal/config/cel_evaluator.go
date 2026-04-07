package config

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// CompiledExpression holds a compiled CEL AST ready for evaluation.
// ASTs are cached for performance; programs are created per-evaluation so that
// the tools.hasPattern function can access the current tools list.
type CompiledExpression struct {
	ast  *cel.Ast
	expr string
}

// String returns the original expression string.
func (c *CompiledExpression) String() string { return c.expr }

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

		// Session variables
		cel.Variable("session.id", cel.StringType),
		cel.Variable("session.name", cel.StringType),
		cel.Variable("session.isChild", cel.BoolType),
		cel.Variable("session.isAutoChild", cel.BoolType),
		cel.Variable("session.parentId", cel.StringType),

		// Parent variables
		cel.Variable("parent.exists", cel.BoolType),
		cel.Variable("parent.name", cel.StringType),
		cel.Variable("parent.acpServer", cel.StringType),

		// Children variables
		cel.Variable("children.count", cel.IntType),
		cel.Variable("children.exists", cel.BoolType),
		cel.Variable("children.names", cel.ListType(cel.StringType)),
		cel.Variable("children.acpServers", cel.ListType(cel.StringType)),

		// Tools variables
		cel.Variable("tools.available", cel.BoolType),
		cel.Variable("tools.names", cel.ListType(cel.StringType)),

		// Custom function: tools.hasPattern(pattern) bool
		// The implementation is injected per-evaluation via cel.Functions ProgramOption.
		cel.Function("tools.hasPattern",
			cel.Overload("tools_hasPattern_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
			),
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

	ce := &CompiledExpression{ast: ast, expr: expression}

	e.mu.Lock()
	e.cache[expression] = ce
	e.mu.Unlock()

	return ce, nil
}

// Evaluate runs a compiled CEL expression against the provided context.
// Returns true if the expression evaluates to true (visible), false otherwise.
// If ctx is nil, defaults to true (visible).
func (e *CELEvaluator) Evaluate(compiled *CompiledExpression, ctx *PromptEnabledContext) (bool, error) {
	if ctx == nil {
		return true, nil
	}

	// Extend the environment per evaluation so tools.hasPattern can close over the tool names.
	// env.Extend() creates a child env inheriting all declarations; we add the binding here.
	evalEnv, err := e.env.Extend(
		cel.Function("tools.hasPattern",
			cel.Overload("tools_hasPattern_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(toolsHasPatternImpl(ctx.Tools.Names)),
			),
		),
	)
	if err != nil {
		return true, fmt.Errorf("cel: extend environment error for %q: %w", compiled.expr, err)
	}
	prog, err := evalEnv.Program(compiled.ast)
	if err != nil {
		return true, fmt.Errorf("cel: program error for %q: %w", compiled.expr, err)
	}

	out, _, err := prog.Eval(buildActivation(ctx))
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
	return map[string]any{
		"acp.name":        ctx.ACP.Name,
		"acp.type":        ctx.ACP.Type,
		"acp.tags":        ctx.ACP.Tags,
		"acp.autoApprove": ctx.ACP.AutoApprove,

		"workspace.uuid":   ctx.Workspace.UUID,
		"workspace.folder": ctx.Workspace.Folder,
		"workspace.name":   ctx.Workspace.Name,

		"session.id":          ctx.Session.ID,
		"session.name":        ctx.Session.Name,
		"session.isChild":     ctx.Session.IsChild,
		"session.isAutoChild": ctx.Session.IsAutoChild,
		"session.parentId":    ctx.Session.ParentID,

		"parent.exists":    ctx.Parent.Exists,
		"parent.name":      ctx.Parent.Name,
		"parent.acpServer": ctx.Parent.ACPServer,

		"children.count":      int64(ctx.Children.Count),
		"children.exists":     ctx.Children.Exists,
		"children.names":      ctx.Children.Names,
		"children.acpServers": ctx.Children.ACPServers,

		"tools.available": ctx.Tools.Available,
		"tools.names":     ctx.Tools.Names,
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

// toolsHasPatternImpl returns a CEL UnaryOp that checks whether any tool name
// in the provided list matches the given glob pattern (e.g., "github_*").
func toolsHasPatternImpl(names []string) func(ref.Val) ref.Val {
	return func(patternVal ref.Val) ref.Val {
		pattern, ok := patternVal.(types.String)
		if !ok {
			return types.Bool(false)
		}
		for _, name := range names {
			matched, err := filepath.Match(string(pattern), name)
			if err == nil && matched {
				return types.Bool(true)
			}
		}
		return types.Bool(false)
	}
}
