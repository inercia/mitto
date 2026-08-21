package cel

import "sync"

// fragmentProvider is a settable hook that returns the current workspace
// prompt fragment registry as a flat name->body map, or nil when no registry
// is installed. It exists so renderNestedPromptBody (used by ReadTemplate and
// PromptTextWithArgs) can attach fragments to its sub-renders WITHOUT
// internal/cel importing internal/prompts — that import direction is
// forbidden (decoupled in mitto-b8k.3) and pinned by
// TestReadTemplate_NoPromptsImport, which AST-scans every non-test file in
// this package for the internal/prompts import path.
//
// internal/prompts installs this hook (see fragments.go's init in that
// package) with a closure that reads through its own CurrentFragments()
// singleton at call time, so a later fragment reload (fs-watcher, test
// install/teardown) is observed automatically without any generation
// plumbing here.
var (
	fragmentProviderMu sync.RWMutex
	fragmentProvider   func() map[string]string
)

// SetFragmentProvider installs fn as the source of fragments for nested
// prompt sub-renders (ReadTemplate, PromptTextWithArgs). Pass nil to clear
// (e.g. in test teardown). Safe for concurrent use.
func SetFragmentProvider(fn func() map[string]string) {
	fragmentProviderMu.Lock()
	defer fragmentProviderMu.Unlock()
	fragmentProvider = fn
}

// fragmentsForNestedRender returns the current fragment set for attaching to
// a nested sub-render, or nil when no provider is installed (or the provider
// itself returns nil/empty — both mean "attach nothing", preserving
// pre-mitto-twa behavior bytewise for callers that never load prompts, e.g.
// standalone internal/cel unit tests).
func fragmentsForNestedRender() map[string]string {
	fragmentProviderMu.RLock()
	fn := fragmentProvider
	fragmentProviderMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}
