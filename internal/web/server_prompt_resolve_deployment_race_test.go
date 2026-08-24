package web

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
)

// TestResolvePromptByName_DeploymentInProgress_ShouldBeTransient reproduces
// mitto-ctf: a loop-prompt resolution that races a builtin-prompts deployment
// (or any cold/uninitialized PromptsCache) is misclassified as a durable
// "not found" instead of a transient error, so the loop-runner strike counter
// (mitto-8bg) counts it toward MaxPromptResolveFailures and auto-pauses a
// perfectly healthy loop.
//
// Root cause (investigate phase, mitto-ctf): resolvePromptByName only Warn-logs
// any error from PromptsCache.GetWebPrompts() and leaves globalFilePrompts nil
// (internal/web/server.go ~3482-3487). When the cache has no last-good snapshot
// to fall back on and a `.mitto-prompts-deploying` marker is active,
// PromptsCache.reload() returns prompts.ErrDeploymentInProgress with an EMPTY
// LoadErrors() list (internal/prompts/cache.go lastGoodOrDeploymentError). The
// resolver's existing transient-race carve-out only inspects LoadErrors() for a
// `template "..." not defined` signature (server.go ~3569-3577), so it never
// fires here, and the resolver falls through to the plain, durable
// `prompt %q not found` (server.go ~3579).
//
// This test plants a prompt file on disk, opens (but does not finish) a
// prompts deployment transaction over its directory via prompts.BeginDeployment
// so the underlying PromptsCache has no last-good snapshot to serve, and then
// resolves the prompt by name. The prompt genuinely exists on disk and should
// only be transiently unresolvable; the reproduction demonstrates that today
// resolvePromptByName instead returns a durable, non-transient error.
func TestResolvePromptByName_DeploymentInProgress_ShouldBeTransient(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	const promptName = "Support: watch channel"
	body := "name: '" + promptName + "'\nprompt: |\n  watch the channel.\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "watch-channel.prompt.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	// Fresh, never-warmed PromptsCache: c.prompts is nil, so
	// lastGoodOrDeploymentError() has no last-good snapshot to fall back on and
	// must return ErrDeploymentInProgress once a deployment marker is active.
	cache := prompts.NewPromptsCache()

	// Open a deployment transaction over promptsDir and deliberately do NOT
	// call finish(): this is the in-flight window (e.g. `mitto prompts
	// update-builtin --force` still running, or the loop firing on a
	// freshly-reconnected session before the cache's first load) that the
	// investigation identified as the trigger.
	finish, err := prompts.BeginDeployment(promptsDir)
	if err != nil {
		t.Fatalf("BeginDeployment: %v", err)
	}
	t.Cleanup(func() { _ = finish() })

	s := &Server{
		config: Config{
			PromptsCache: cache,
			MittoConfig:  &configPkg.Config{},
		},
	}

	// Sanity check: the cache itself does surface ErrDeploymentInProgress with
	// no LoadErrors, confirming the precondition this bug depends on.
	if _, err := cache.GetWebPrompts(); !errors.Is(err, prompts.ErrDeploymentInProgress) {
		t.Fatalf("GetWebPrompts() error = %v, want errors.Is(err, prompts.ErrDeploymentInProgress)", err)
	}
	if loadErrs := cache.LoadErrors(); len(loadErrs) != 0 {
		t.Fatalf("LoadErrors() = %v, want empty (deployment-in-progress path records no load errors)", loadErrs)
	}

	_, resolveErr := s.resolvePromptByName(promptName, "/work")
	if resolveErr == nil {
		t.Fatalf("resolvePromptByName(%q) succeeded despite an in-progress deployment with no last-good snapshot; want an error", promptName)
	}

	// This is the bug: a resolution failure that stems purely from an
	// in-progress deployment (no genuine "prompt missing/renamed" signal) must
	// be classified transient, exactly like the existing fragment-compile-race
	// carve-out, so the loop-runner strike counter (mitto-8bg) does not count
	// it toward auto-pausing the loop. Today it is not.
	if !errors.Is(resolveErr, conversation.ErrPromptTransientCompileRace) {
		t.Fatalf("resolvePromptByName(%q) error = %v; want errors.Is(err, conversation.ErrPromptTransientCompileRace) (mitto-ctf: deployment-in-progress miss must be classified transient, not durable)", promptName, resolveErr)
	}
}
