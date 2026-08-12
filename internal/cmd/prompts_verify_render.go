package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/cel"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
)

// verify flags
var (
	verifyExtraDir string
	verifyVerbose  bool
)

// render flags
var (
	renderArgs         []string
	renderACP          string
	renderWorkspaceDir string
	renderOutputFile   string
	renderExtraDir     string
)

var promptsVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify prompts and fragments in MITTO_DIR/prompts",
	Long: `Statically verify every prompt and fragment in the prompts tree.

Loads (in priority order, matching runtime):
  1. MITTO_DIR/prompts/builtin/  (builtin prompts + fragments)
  2. MITTO_DIR/prompts/          (user-installed prompts + fragments)
  3. --dir <path>                (optional extra directory, e.g. a workspace's .mitto/prompts)

Checks:
  - Fragment files parse cleanly (*.tmpl)
  - Prompt YAML parses and validates against schema
  - Every {{ template "..." }} reference resolves to a known fragment
  - Template Cond/When CEL expressions precompile
  - No duplicate prompt names within the merged tree

Exits with status 1 if any error is found. Use --verbose to also print
successful entries.`,
	RunE: runPromptsVerify,
}

var promptsRenderCmd = &cobra.Command{
	Use:   "render <prompt-name>",
	Short: "Render a prompt with CLI-supplied variables",
	Long: `Render a named prompt end-to-end (including fragment expansion) and print
the result that would be sent to the agent.

Loads the same three tiers as ` + "`prompts verify`" + `, so fragments resolve.
Looks up the prompt by exact name (case-insensitive fallback).

Context defaults:
  Args:            from --arg K=V (repeatable)
  Workspace.Folder: --workspace-dir (default: current working directory)
  ACP.Name:        --acp           (default: "auggie")
  Session.ID:      auto-generated placeholder ("render-<unix>")
  All other fields: zero-valued (safe per PromptEnabledContext contract)

Example:
  mitto prompts render "Commit changes" --arg Scope=web --arg Message="fix bug"
  mitto prompts render beads-issue-feature-phase-implement --arg Tier=Coding -o /tmp/out.txt`,
	Args: cobra.ExactArgs(1),
	RunE: runPromptsRender,
}

func init() {
	promptsCmd.AddCommand(promptsVerifyCmd)
	promptsCmd.AddCommand(promptsRenderCmd)

	promptsVerifyCmd.Flags().StringVar(&verifyExtraDir, "dir", "",
		"Additional prompts directory to include (loaded last, wins on name collisions)")
	promptsVerifyCmd.Flags().BoolVarP(&verifyVerbose, "verbose", "v", false,
		"Also print successfully-loaded prompts and fragments")

	promptsRenderCmd.Flags().StringArrayVar(&renderArgs, "arg", nil,
		"Prompt argument in K=V form (repeatable, e.g. --arg Scope=web --arg Message=fix)")
	promptsRenderCmd.Flags().StringVar(&renderACP, "acp", "auggie",
		"ACP server name to expose as .ACP.Name in the render context")
	promptsRenderCmd.Flags().StringVar(&renderWorkspaceDir, "workspace-dir", "",
		"Workspace directory exposed as .Workspace.Folder (default: current directory)")
	promptsRenderCmd.Flags().StringVarP(&renderOutputFile, "output", "o", "",
		"Write rendered output to this file instead of stdout")
	promptsRenderCmd.Flags().StringVar(&renderExtraDir, "dir", "",
		"Additional prompts directory to include (loaded last, wins on name collisions)")
}

// promptTreeDirs returns the directories to load, split by concern to match
// the runtime wiring (internal/web/server.go: getPromptsWatchDirs vs
// getFragmentScanDirs):
//
//   - promptDirs is the list scanned for *.prompt.yaml. Only PromptsDir() is
//     listed because its recursive walk already descends into builtin/.
//     Listing both would double-load every builtin prompt.
//   - fragmentDirs is the list scanned for *.tmpl. BuiltinPromptsDir() MUST be
//     its own scan root so fragment names resolve as `_shared/…` (not
//     `builtin/_shared/…`); PromptsDir() is added for hand-authored fragments
//     dropped directly under MITTO_DIR/prompts/ outside builtin/.
//
// The optional extraDir is appended to both lists (later wins on collisions).
// Missing directories are tolerated by the loaders themselves.
func promptTreeDirs(extraDir string) (promptDirs, fragmentDirs []string, err error) {
	builtinDir, err := appdir.BuiltinPromptsDir()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve builtin prompts dir: %w", err)
	}
	globalDir, err := appdir.PromptsDir()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve global prompts dir: %w", err)
	}
	promptDirs = []string{globalDir}
	fragmentDirs = []string{builtinDir, globalDir}
	if extraDir != "" {
		abs, err := filepath.Abs(extraDir)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve --dir: %w", err)
		}
		promptDirs = append(promptDirs, abs)
		fragmentDirs = append(fragmentDirs, abs)
	}
	return promptDirs, fragmentDirs, nil
}

// installFragments loads and installs the merged fragment registry across dirs.
// Returns the per-file fragment load errors so callers can report them.
func installFragments(dirs []string) ([]config.FragmentLoadError, error) {
	reg, fragErrs, err := prompts.ReloadFragmentsFromDirs(dirs)
	if err != nil {
		return fragErrs, fmt.Errorf("load fragments: %w", err)
	}
	prompts.SetCurrentFragments(reg)
	return fragErrs, nil
}

// loadAllPrompts loads prompts from every dir with per-file errors surfaced.
// Prompts with duplicate names are kept in-order (later dirs override earlier
// ones) so the caller can report collisions.
func loadAllPrompts(dirs []string) ([]dirPromptResult, error) {
	var results []dirPromptResult
	for _, dir := range dirs {
		ps, loadErrs, err := config.LoadPromptsFromDirWithErrors(dir)
		if err != nil {
			return results, fmt.Errorf("walk %s: %w", dir, err)
		}
		results = append(results, dirPromptResult{
			Dir:        dir,
			Prompts:    ps,
			LoadErrors: loadErrs,
		})
	}
	return results, nil
}

type dirPromptResult struct {
	Dir        string
	Prompts    []*config.PromptFile
	LoadErrors []config.PromptLoadError
}

// promptTreeVerification is the result of loading a prompt tree exactly as the
// runtime does: fragments first, then every prompt against that registry.
type promptTreeVerification struct {
	FragmentCount  int
	FragmentErrors []config.FragmentLoadError
	DirResults     []dirPromptResult
	PromptCount    int
	PromptErrors   int
}

func verifyPromptTree(promptDirs, fragmentDirs []string) (promptTreeVerification, error) {
	var result promptTreeVerification

	fragErrs, err := installFragments(fragmentDirs)
	if err != nil {
		return result, err
	}
	result.FragmentErrors = fragErrs
	if frags := prompts.CurrentFragments(); frags != nil {
		result.FragmentCount = frags.Len()
	}

	dirResults, err := loadAllPrompts(promptDirs)
	if err != nil {
		return result, err
	}
	result.DirResults = dirResults
	for _, r := range dirResults {
		result.PromptCount += len(r.Prompts)
		result.PromptErrors += len(r.LoadErrors)
	}

	return result, nil
}

func (r promptTreeVerification) validationError() error {
	if len(r.FragmentErrors) == 0 && r.PromptErrors == 0 {
		return nil
	}
	return fmt.Errorf("verification failed: %d fragment error(s), %d prompt error(s)",
		len(r.FragmentErrors), r.PromptErrors)
}

func runPromptsVerify(cmd *cobra.Command, args []string) error {
	promptDirs, fragmentDirs, err := promptTreeDirs(verifyExtraDir)
	if err != nil {
		return err
	}

	fmt.Println("Prompt directories (later wins on name collision):")
	for i, d := range promptDirs {
		status := ""
		if _, err := os.Stat(d); os.IsNotExist(err) {
			status = "  [missing, skipped]"
		}
		fmt.Printf("  %d. %s%s\n", i+1, d, status)
	}
	fmt.Println()
	fmt.Println("Fragment scan roots:")
	for i, d := range fragmentDirs {
		status := ""
		if _, err := os.Stat(d); os.IsNotExist(err) {
			status = "  [missing, skipped]"
		}
		fmt.Printf("  %d. %s%s\n", i+1, d, status)
	}
	fmt.Println()

	// Load fragments first, then load every prompt against that registry.
	verification, err := verifyPromptTree(promptDirs, fragmentDirs)
	if err != nil {
		return err
	}
	fragErrs := verification.FragmentErrors
	dirResults := verification.DirResults
	frags := prompts.CurrentFragments()
	fragCount := verification.FragmentCount
	fmt.Printf("Fragments loaded: %d\n", fragCount)
	if len(fragErrs) > 0 {
		fmt.Printf("Fragment errors:  %d\n", len(fragErrs))
	}

	totalPrompts := verification.PromptCount
	totalPromptErrors := verification.PromptErrors
	fmt.Printf("Prompts loaded:   %d\n", totalPrompts)
	if totalPromptErrors > 0 {
		fmt.Printf("Prompt errors:    %d\n", totalPromptErrors)
	}
	fmt.Println()

	// 3. Report fragment load errors.
	if len(fragErrs) > 0 {
		fmt.Println("Fragment load errors:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  FILE\tERROR")
		for _, e := range fragErrs {
			fmt.Fprintf(w, "  %s\t%v\n", e.Path, e.Err)
		}
		w.Flush()
		fmt.Println()
	}

	// 4. Report prompt load errors (parse, schema, precompile — including
	//    missing-fragment refs which PrecompileTemplateConds catches).
	if totalPromptErrors > 0 {
		fmt.Println("Prompt load errors:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  DIR\tFILE\tERROR")
		for _, r := range dirResults {
			for _, e := range r.LoadErrors {
				fmt.Fprintf(w, "  %s\t%s\t%v\n", filepath.Base(r.Dir), e.Path, e.Err)
			}
		}
		w.Flush()
		fmt.Println()
	}

	// 5. Detect duplicate prompt names across the merged tree. Later dirs
	//    override earlier ones by design, but the user probably wants to see
	//    it (esp. when running with --dir).
	nameOrigins := map[string][]string{}
	for _, r := range dirResults {
		for _, p := range r.Prompts {
			nameOrigins[p.Name] = append(nameOrigins[p.Name],
				filepath.Join(filepath.Base(r.Dir), p.Path))
		}
	}
	var duplicates []string
	for name, origins := range nameOrigins {
		if len(origins) > 1 {
			duplicates = append(duplicates, name)
		}
	}
	if len(duplicates) > 0 {
		sort.Strings(duplicates)
		fmt.Println("Prompt-name collisions (later-dir prompt wins at runtime):")
		for _, name := range duplicates {
			fmt.Printf("  %q\n", name)
			for _, origin := range nameOrigins[name] {
				fmt.Printf("      - %s\n", origin)
			}
		}
		fmt.Println()
	}

	// 6. Verbose mode: print all successfully-loaded prompts + fragments.
	if verifyVerbose {
		if fragCount > 0 {
			fmt.Println("Loaded fragments:")
			names := make([]string, 0, fragCount)
			for name := range frags.All() {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Printf("  %s\n", n)
			}
			fmt.Println()
		}
		if totalPrompts > 0 {
			fmt.Println("Loaded prompts:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  NAME\tDIR\tFILE")
			for _, r := range dirResults {
				for _, p := range r.Prompts {
					fmt.Fprintf(w, "  %s\t%s\t%s\n", p.Name, filepath.Base(r.Dir), p.Path)
				}
			}
			w.Flush()
			fmt.Println()
		}
	}

	if verification.validationError() != nil {
		fmt.Println("VERIFY FAILED")
		return verification.validationError()
	}
	fmt.Println("VERIFY OK")
	return nil
}

func runPromptsRender(cmd *cobra.Command, args []string) error {
	promptName := args[0]

	promptDirs, fragmentDirs, err := promptTreeDirs(renderExtraDir)
	if err != nil {
		return err
	}

	// Install fragments first so subsequent renders can resolve `{{ template
	// "..." }}` references. Silent on per-file fragment errors here — those
	// are `verify`'s job.
	if _, err := installFragments(fragmentDirs); err != nil {
		return err
	}

	// Load all prompts across dirs; later-dir wins on name collision.
	byName := map[string]*config.PromptFile{}
	byNameLower := map[string]*config.PromptFile{}
	dirResults, err := loadAllPrompts(promptDirs)
	if err != nil {
		return err
	}
	for _, r := range dirResults {
		for _, p := range r.Prompts {
			byName[p.Name] = p
			byNameLower[strings.ToLower(p.Name)] = p
		}
	}

	// Lookup: exact match first, then case-insensitive.
	target := byName[promptName]
	if target == nil {
		target = byNameLower[strings.ToLower(promptName)]
	}
	if target == nil {
		available := make([]string, 0, len(byName))
		for name := range byName {
			available = append(available, name)
		}
		sort.Strings(available)
		return fmt.Errorf("prompt %q not found (loaded %d prompts). Use `mitto prompts list` or `mitto prompts verify -v` to see available names",
			promptName, len(available))
	}

	// Parse --arg K=V into a map.
	argMap, err := parseKVArgs(renderArgs)
	if err != nil {
		return err
	}

	// Resolve --workspace-dir (default: CWD).
	workspaceDir := renderWorkspaceDir
	if workspaceDir == "" {
		workspaceDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve current working directory: %w", err)
		}
	} else {
		workspaceDir, err = filepath.Abs(workspaceDir)
		if err != nil {
			return fmt.Errorf("resolve --workspace-dir: %w", err)
		}
	}

	// Build a minimal render context. All fields left zero are documented
	// safe on PromptEnabledContext.
	ctx := &cel.PromptEnabledContext{
		ACP: cel.ACPContext{
			Name: renderACP,
			Type: renderACP,
		},
		Workspace: cel.WorkspaceContext{
			Folder: workspaceDir,
		},
		Session: cel.SessionContext{
			ID: fmt.Sprintf("render-%d", time.Now().Unix()),
		},
		Args: argMap,
	}

	// Render with the full template FuncMap.
	fm := cel.BuildTemplateFuncMap(ctx)
	rendered, err := prompts.RenderPromptTemplate(target.Name, target.Content, ctx, fm)
	if err != nil {
		return fmt.Errorf("render prompt %q: %w", target.Name, err)
	}

	// Emit.
	if renderOutputFile != "" {
		if err := os.WriteFile(renderOutputFile, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", renderOutputFile, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %d bytes to %s\n", len(rendered), renderOutputFile)
		return nil
	}
	// Ensure a trailing newline for terminal-friendliness without corrupting
	// bodies that already end with one.
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	fmt.Print(rendered)
	return nil
}

// parseKVArgs parses a list of "KEY=VALUE" strings into a map. Empty keys and
// missing "=" separators are rejected. Values may contain "=" (only the first
// separator counts).
func parseKVArgs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("--arg %q: expected KEY=VALUE with non-empty KEY", p)
		}
		key := p[:eq]
		val := p[eq+1:]
		out[key] = val
	}
	return out, nil
}
