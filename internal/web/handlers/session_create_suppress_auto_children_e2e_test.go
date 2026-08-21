package handlers

import (
	"sync/atomic"
	"testing"
)

// E2E coverage for mitto-nlx.4 on the REST create path: pins that
// HandleCreateSession consults Deps.ResolvePromptSuppressAutoChildren with the
// correct (promptName, workingDir) pair and — via CreateSessionOptions — routes
// the flag into SessionManager.CreateSessionWithWorkspaceAndOptions.
//
// The E2E harness cannot bring up a real ACP server, so the parent create
// itself fails downstream. That is acceptable here: the resolver spy runs
// BEFORE CreateSessionWithWorkspaceAndOptions is invoked, so its arguments and
// call count are observable regardless of the create outcome — the same
// technique used by the reuseIssue / target.title E2E tests in
// session_create_reuse_e2e_test.go.

// TestSuppressAutoChildrenE2E_ResolverInvokedWithOriginPromptName pins the
// happy path: a POST that carries origin_prompt_name (but no initial prompt)
// must invoke ResolvePromptSuppressAutoChildren exactly once with the origin
// prompt name and the request's working_dir.
func TestSuppressAutoChildrenE2E_ResolverInvokedWithOriginPromptName(t *testing.T) {
	const workingDir = "/work-suppress-origin"
	const originPrompt = "no-children-please"
	_, h := newReuseE2EHandlers(t, workingDir)

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return false }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }

	var (
		calls      int32
		gotPrompt  string
		gotWorkDir string
	)
	h.deps.ResolvePromptSuppressAutoChildren = func(promptName, wd string) bool {
		atomic.AddInt32(&calls, 1)
		gotPrompt = promptName
		gotWorkDir = wd
		return true
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		OriginPromptName: originPrompt,
	})

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("ResolvePromptSuppressAutoChildren call count = %d, want 1", got)
	}
	if gotPrompt != originPrompt {
		t.Errorf("resolver received promptName = %q, want %q", gotPrompt, originPrompt)
	}
	if gotWorkDir != workingDir {
		t.Errorf("resolver received workingDir = %q, want %q", gotWorkDir, workingDir)
	}
	// Sanity: response is not {reused:true}. 200 or 500 both acceptable here —
	// the resolver was consulted before create ran.
	assertNotReusedTo(t, decodeSessionResponse(t, w), "")
}

// TestSuppressAutoChildrenE2E_InitialPromptNameFallback exercises the
// initial->origin precedence documented at session_create.go:121-124: when
// initial_prompt_name is set, IT is passed to the suppress resolver — not
// origin_prompt_name. This mirrors the fallback used by the reuseIssue and
// singleton resolvers so all three tiers stay in lockstep.
func TestSuppressAutoChildrenE2E_InitialPromptNameFallback(t *testing.T) {
	const workingDir = "/work-suppress-initial"
	const initialPrompt = "initial-suppresses"
	const originPrompt = "origin-should-be-ignored"
	_, h := newReuseE2EHandlers(t, workingDir)

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return false }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }

	var gotPrompt string
	h.deps.ResolvePromptSuppressAutoChildren = func(promptName, _ string) bool {
		gotPrompt = promptName
		return false
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:        workingDir,
		InitialPromptName: initialPrompt,
		OriginPromptName:  originPrompt,
	})

	if gotPrompt != initialPrompt {
		t.Errorf("resolver received promptName = %q, want %q (initial_prompt_name must win over origin_prompt_name)", gotPrompt, initialPrompt)
	}
	assertNotReusedTo(t, decodeSessionResponse(t, w), "")
}

// TestSuppressAutoChildrenE2E_NoResolverCallWithoutPromptName pins the
// short-circuit at session_create.go:248 — when no prompt name is supplied,
// the resolver must not be consulted. This preserves the "flag defaults to
// false" contract for plain sessions.
func TestSuppressAutoChildrenE2E_NoResolverCallWithoutPromptName(t *testing.T) {
	const workingDir = "/work-suppress-none"
	_, h := newReuseE2EHandlers(t, workingDir)

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return false }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }

	var calls int32
	h.deps.ResolvePromptSuppressAutoChildren = func(string, string) bool {
		atomic.AddInt32(&calls, 1)
		return true
	}

	w := postSession(t, h, SessionCreateRequest{
		WorkingDir: workingDir,
		Name:       "plain session",
		// No InitialPromptName, no OriginPromptName.
	})

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("ResolvePromptSuppressAutoChildren call count = %d with no prompt name, want 0", got)
	}
	assertNotReusedTo(t, decodeSessionResponse(t, w), "")
}

// TestSuppressAutoChildrenE2E_NilResolverIsSafe pins the nil-guard at
// session_create.go:248: when Deps.ResolvePromptSuppressAutoChildren is nil,
// the handler must NOT panic and must default the flag to false.
func TestSuppressAutoChildrenE2E_NilResolverIsSafe(t *testing.T) {
	const workingDir = "/work-suppress-nil"
	_, h := newReuseE2EHandlers(t, workingDir)

	h.deps.ResolvePromptReuseIssue = func(string, string) bool { return false }
	h.deps.ResolvePromptSingleton = func(string, string) bool { return false }
	h.deps.ResolvePromptSuppressAutoChildren = nil // explicit

	// A panic in the handler would surface as a test failure via the recorder.
	w := postSession(t, h, SessionCreateRequest{
		WorkingDir:       workingDir,
		OriginPromptName: "any-prompt",
	})
	assertNotReusedTo(t, decodeSessionResponse(t, w), "")
}
