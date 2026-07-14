package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

var (
	// prompt-command flags
	promptWithMCPServer bool
	promptMCPPort       int
	promptTimeout       time.Duration
	promptConcurrent    int
	promptWithAux       bool
	promptAuxFork       bool
	promptWaitAux       bool
)

// promptCmd represents the prompt command: a single-shot debugging harness.
//
// It simulates one Mitto conversation — connect, initialize, session/new,
// send one prompt, stream the response, exit — while recording a cold-start
// phase timeline (spawn → initialize → session_new → first_token → done) that
// is printed at the end. This isolates agent-side MCP-init behaviour and (with
// --with-mcp-server) the Mitto-side /mcp handshake from the noise of the web UI
// and concurrent sessions, for debugging cold-start wedges (mitto-54k).
var promptCmd = &cobra.Command{
	Use:   "prompt [flags] \"<prompt text>\"",
	Short: "Send a single prompt to an ACP server and print a cold-start timeline (debugging)",
	Long: `Simulate a single-shot Mitto conversation for debugging.

Creates a session in the current folder (or --dir), uses the selected ACP
server (--acp, or the config default), sends one prompt, streams the agent's
response to stdout, then prints a cold-start phase timeline and exits.

Primary use: reproduce and measure cold-start / MCP-init wedges (mitto-54k)
without the web UI or concurrent-session noise.

Examples:
  mitto prompt "what is the capital of France"
  mitto prompt --acp 'Auggie (Opus)' "say hello"

  # Also stand up Mitto's in-process HTTP MCP server on :5757 so the agent's
  # http://127.0.0.1:5757/mcp target resolves (reproduces the Mitto-side
  # /mcp handshake path, e.g. the SSE-stall wedge mitto-6hr):
  mitto prompt --with-mcp-server "say hello"

  # Reproduce the auxiliary-session prewarm storm that a real cold start also
  # incurs (title-gen, follow-up, mcp-check, mcp-tools sessions multiplexed
  # onto the same ACP process, single-worker + staggered per the production
  # AuxPrewarmSchedule). Runs concurrently with the main prompt:
  mitto prompt --with-aux "say hello"`,
	Args: cobra.ExactArgs(1),
	RunE: runPrompt,
}

func init() {
	rootCmd.AddCommand(promptCmd)

	promptCmd.Flags().BoolVar(&promptWithMCPServer, "with-mcp-server", false,
		"Start Mitto's in-process HTTP MCP server so the agent's :5757/mcp target resolves")
	promptCmd.Flags().IntVar(&promptMCPPort, "mcp-port", mcpserver.DefaultPort,
		"Port for the in-process MCP server (only with --with-mcp-server)")
	promptCmd.Flags().DurationVar(&promptTimeout, "timeout", 0,
		"Abort if the prompt does not complete within this duration (0 = no timeout)")
	promptCmd.Flags().IntVar(&promptConcurrent, "concurrent", 0,
		"Keep N concurrent MCP client sessions polling the in-process /mcp server during the prompt "+
			"(load to reproduce the SSE-stall wedge; requires --with-mcp-server)")
	promptCmd.Flags().BoolVar(&promptWithAux, "with-aux", false,
		"Also create Mitto's auxiliary sessions (title-gen, follow-up, mcp-check, mcp-tools) on the "+
			"same ACP process, single-worker + staggered per the production schedule, concurrently with "+
			"the main prompt (reproduces the cold-start aux-session prewarm storm)")
	promptCmd.Flags().BoolVar(&promptAuxFork, "aux-fork", false,
		"Use the fork-per-session (Claude Code) aux prewarm stagger instead of the multiplex (Auggie) "+
			"default when --with-aux is set")
	promptCmd.Flags().BoolVar(&promptWaitAux, "wait-aux", false,
		"After the main prompt completes, wait for the full auxiliary-session storm to finish instead "+
			"of cancelling it at teardown (required to let the later-staggered sessions, e.g. title-gen "+
			"@8s / follow-up @12s, actually run; otherwise a fast single-shot exits before they fire)")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	promptText := args[0]

	// Load configuration for this run. The prompt command is exempt from the
	// standard PersistentPreRunE config load (see root.go), so we load it here
	// in a way that NEVER touches web authentication or the macOS Keychain and
	// then strip the web configuration outright — this command must never start
	// the web server nor read any web-auth secret.
	if err := loadPromptConfig(); err != nil {
		return err
	}

	server, err := getSelectedServer()
	if err != nil {
		return err
	}

	// Resolve working directory (first --dir, else current folder).
	workDir, err := resolvePromptWorkDir()
	if err != nil {
		return err
	}

	fmt.Printf("🚀 ACP server: %s\n", server.Name)
	fmt.Printf("   Command:    %s\n", server.Command)
	fmt.Printf("   Working dir: %s\n", workDir)

	// A logger is always used here so the coldstart tracer emits its
	// cold_start_phase / cold_start_summary lines; level follows --debug.
	logger := promptLogger()

	// Context with optional timeout + signal cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if promptTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, promptTimeout)
		defer cancel()
	}
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\n👋 Cancelling...")
		cancel()
	}()

	if promptConcurrent > 0 && !promptWithMCPServer {
		return fmt.Errorf("--concurrent requires --with-mcp-server")
	}

	// Optionally start Mitto's in-process HTTP MCP server (mitto-6hr repro).
	if promptWithMCPServer {
		stop, err := startInProcessMCPServer(ctx, promptMCPPort, logger)
		if err != nil {
			return fmt.Errorf("failed to start in-process MCP server: %w", err)
		}
		defer stop()

		// Optionally hold N concurrent MCP client sessions against the server
		// for the duration of the run, to reproduce the concurrency that
		// triggers the standalone-SSE stall (mitto-6hr).
		if promptConcurrent > 0 {
			loadCtx, loadCancel := context.WithCancel(ctx)
			defer loadCancel()
			startMCPLoad(loadCtx, promptMCPPort, promptConcurrent, logger)
		}
	}

	return runPromptOnce(ctx, server, workDir, promptText, logger)
}

// loadPromptConfig loads the configuration used by the prompt command into the
// package-level cfg, deliberately avoiding every web-authentication code path:
//
//   - With --config, it uses config.Load (a pure file parse, no Keychain).
//   - Otherwise it uses config.LoadSettingsWithFallbackNoKeychain, which merges
//     settings.json + ~/.mittorc exactly like the normal loader but never reads
//     the external-access password from secure storage.
//
// After loading it clears cfg.Web entirely so no web server can be configured
// and no web-auth secret can be present, honouring the invariant that `mitto
// prompt` never starts the web nor touches web auth.
func loadPromptConfig() error {
	if configPath != "" {
		loaded, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration from %s: %w", configPath, err)
		}
		cfg = loaded
		configResult = &config.LoadResult{
			Config:     cfg,
			Source:     config.ConfigSourceCustomFile,
			SourcePath: configPath,
		}
	} else {
		result, err := config.LoadSettingsWithFallbackNoKeychain()
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}
		configResult = result
		cfg = result.Config
	}

	// Strip all web configuration: this command has no HTTP surface, so drop it
	// wholesale to guarantee no web server is started and no web-auth secret is
	// ever carried in the in-memory config.
	if cfg != nil {
		cfg.Web = config.WebConfig{}
	}
	return nil
}

// resolvePromptWorkDir returns the working directory for the session: the first
// --dir flag if provided, otherwise the current directory.
func resolvePromptWorkDir() (string, error) {
	if len(dirFlags) > 0 {
		abs, err := filepath.Abs(stripServerPrefix(dirFlags[0]))
		if err != nil {
			return "", fmt.Errorf("failed to resolve --dir %q: %w", dirFlags[0], err)
		}
		return abs, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return ".", nil
	}
	return wd, nil
}

// promptLogger builds the logger used for the run. It always returns a non-nil
// logger so cold-start phases are emitted; --debug raises the level to DEBUG.
func promptLogger() *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// stripServerPrefix removes an optional "server:" prefix from a --dir value,
// mirroring parseWorkspaces but returning just the path (Windows drive letters
// like C:\ are left intact).
func stripServerPrefix(dirFlag string) string {
	if idx := indexColon(dirFlag); idx > 1 {
		return dirFlag[idx+1:]
	}
	return dirFlag
}

// indexColon returns the index of the first ':' that is not a Windows drive
// letter separator, or -1.
func indexColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			// Skip a single leading drive letter (e.g. "C:").
			if i == 1 {
				continue
			}
			return i
		}
	}
	return -1
}

// runPromptOnce performs the connect → initialize → session/new → prompt flow,
// recording each boundary on a cold-start Trace and printing a timeline.
func runPromptOnce(ctx context.Context, server *config.ACPServer, workDir, promptText string, logger *slog.Logger) error {
	trace := coldstart.New(logger, "cli-prompt", "")

	// first-token detection: wrap the ACP output callback so we can record the
	// first_token phase the moment the agent streams any response text.
	var firstToken atomic.Bool
	output := func(msg string) {
		if firstToken.CompareAndSwap(false, true) {
			trace.Phase("first_token")
		}
		fmt.Print(msg)
	}

	// "begin" starts the trace clock (Δ=0); subsequent phases measure the
	// elapsed time OF the step they name.
	trace.Phase("begin")
	conn, err := acp.NewConnection(ctx, server.Command, server.Cwd, server.Env, true, output, logger, nil)
	if err != nil {
		trace.Summary("spawn_failed")
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()
	trace.Phase("spawn")

	if err := conn.Initialize(ctx); err != nil {
		trace.Summary("initialize_failed")
		return err
	}
	trace.Phase("initialize")

	if err := conn.NewSession(ctx, workDir); err != nil {
		trace.Summary("session_new_failed")
		return err
	}
	trace.Phase("session_new")

	// Optionally reproduce the auxiliary-session prewarm storm concurrently with
	// the main prompt, exactly as a real cold start does (mitto-cgc). Launched in
	// the background so it overlaps the main Prompt round-trip and competes for
	// the shared ACP process, which is the point of the simulation.
	//
	// A fast single-shot main prompt finishes in a few seconds, well before the
	// later-staggered aux sessions (title-gen @8s, follow-up @12s) fire. Without
	// --wait-aux, auxCancel() at teardown truncates the storm, so it never
	// develops. With --wait-aux we block on the storm after the main prompt so
	// the full staggered schedule runs and contends, matching production where
	// the aux storm lives for the whole process lifetime.
	auxDone := make(chan struct{})
	var auxCancel context.CancelFunc
	if promptWithAux {
		var auxCtx context.Context
		auxCtx, auxCancel = context.WithCancel(ctx)
		defer auxCancel()
		go func() {
			defer close(auxDone)
			runAuxPrewarm(auxCtx, conn, workDir, logger)
		}()
	} else {
		close(auxDone)
	}

	if err := conn.Prompt(ctx, promptText); err != nil {
		trace.Summary("prompt_failed")
		return fmt.Errorf("prompt error: %w", err)
	}
	trace.Phase("done")
	trace.Summary("ok")

	// If requested, let the aux storm complete before tearing down the process,
	// so the later-staggered sessions actually run and their contention is
	// observable. Bounded by the outer ctx (and --timeout) so it can't hang.
	if promptWithAux && promptWaitAux {
		fmt.Println("⏳ Waiting for auxiliary-session storm to finish (--wait-aux)...")
		select {
		case <-auxDone:
			fmt.Println("✅ Auxiliary-session storm complete.")
		case <-ctx.Done():
			fmt.Println("⚠️  Context ended before auxiliary storm finished (timeout/cancel).")
		}
	}

	fmt.Println()
	printPromptTimeline(trace)
	return nil
}

// auxPromptFor returns a representative prompt for an auxiliary purpose, so the
// simulated aux session performs a real ACP round-trip like production does
// (not just session creation). The exact wording mirrors the embedded auxiliary
// prompt templates closely enough to exercise the same agent path.
func auxPromptFor(purpose string) string {
	switch purpose {
	case auxiliary.PurposeTitleGen:
		return fmt.Sprintf(auxiliary.GenerateTitlePromptTemplate, "simulated cold-start prompt")
	case auxiliary.PurposeFollowUp:
		return fmt.Sprintf(auxiliary.AnalyzeFollowUpQuestionsPromptTemplate,
			"simulated user prompt", "simulated agent response")
	case auxiliary.PurposeMCPCheck:
		url := fmt.Sprintf("http://127.0.0.1:%d/mcp", promptMCPPort)
		return fmt.Sprintf(auxiliary.CheckMCPAvailabilityPromptTemplate, url, url)
	case auxiliary.PurposeMCPTools:
		return auxiliary.FetchMCPToolsPromptTemplate
	default:
		return "reply with: ok"
	}
}

// runAuxPrewarm reproduces the production auxiliary-session prewarm storm: a
// single worker walks the staggered AuxPrewarmSchedule, and for each purpose
// creates an ADDITIONAL session on the same ACP process (mirroring
// acpproc.prewarmAuxiliarySessions) then sends the purpose's representative
// prompt. Serialized creation guarantees at most one session/new in flight at a
// time, matching mitto-cgc. Best-effort: per-purpose errors are logged at DEBUG
// and do not abort the schedule or the main prompt.
func runAuxPrewarm(ctx context.Context, conn *acp.Connection, workDir string, logger *slog.Logger) {
	var pc *config.PrewarmConfig
	if cfg != nil {
		pc = cfg.Prewarm
	}
	schedule := pc.AuxPrewarmSchedule(promptAuxFork)
	fmt.Printf("🧩 Simulating %d auxiliary session(s) (fork=%v): ", len(schedule), promptAuxFork)
	for i, e := range schedule {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s@%dms", e.Purpose, e.Delay.Milliseconds())
	}
	fmt.Println()

	start := time.Now()
	for _, entry := range schedule {
		target := start.Add(entry.Delay)
		if remaining := time.Until(target); remaining > 0 {
			select {
			case <-time.After(remaining):
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}

		sessCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		sid, err := conn.NewAuxSession(sessCtx, workDir)
		if err != nil {
			cancel()
			if logger != nil {
				logger.Debug("aux session create failed", "purpose", entry.Purpose, "error", err)
			}
			continue
		}
		if logger != nil {
			logger.Debug("aux session created", "purpose", entry.Purpose, "session_id", string(sid))
		}
		if err := conn.PromptSession(sessCtx, sid, auxPromptFor(entry.Purpose)); err != nil {
			if logger != nil {
				logger.Debug("aux session prompt failed", "purpose", entry.Purpose, "error", err)
			}
		}
		cancel()
	}
}

// printPromptTimeline prints the most recent cold-start summary (the one just
// finalized by trace.Summary) as a human-readable phase table.
func printPromptTimeline(trace *coldstart.Trace) {
	sums := coldstart.RecentSummaries(1)
	if len(sums) == 0 {
		return
	}
	s := sums[0]
	fmt.Println("\n⏱  Cold-start timeline")
	fmt.Printf("   id=%s outcome=%s total=%dms\n", s.ID, s.Outcome, s.TotalMs)
	for _, p := range s.Phases {
		fmt.Printf("   %-14s +%6dms (Δ%6dms)\n", p.Name, p.ElapsedMs, p.PhaseMs)
	}
}

// startInProcessMCPServer stands up Mitto's HTTP MCP server (SSE/Streamable
// HTTP transport) in-process so the agent's http://127.0.0.1:<port>/mcp target
// resolves against THIS process — reproducing the Mitto-side /mcp handshake
// path (e.g. the SSE-stall wedge mitto-6hr). Returns a stop function.
func startInProcessMCPServer(ctx context.Context, port int, logger *slog.Logger) (func(), error) {
	sessionsDir, err := appdir.SessionsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}
	store, err := session.NewStore(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create session store: %w", err)
	}

	promptsCache := config.NewPromptsCache()
	if cfg != nil && len(cfg.PromptsDirs) > 0 {
		promptsCache.SetAdditionalDirs(cfg.PromptsDirs)
	}

	srv, err := mcpserver.NewServer(
		mcpserver.Config{Host: "127.0.0.1", Port: port, Mode: mcpserver.TransportModeSSE},
		mcpserver.Dependencies{Store: store, Config: cfg, PromptsCache: promptsCache},
	)
	if err != nil {
		store.Close()
		return nil, err
	}
	if err := srv.Start(ctx); err != nil {
		store.Close()
		return nil, err
	}
	fmt.Printf("🔌 In-process MCP server listening on http://%s:%d/mcp\n", srv.Host(), srv.Port())

	stop := func() {
		_ = srv.Stop()
		store.Close()
	}
	return stop, nil
}

// startMCPLoad launches n background MCP client sessions that connect to the
// in-process /mcp server and repeatedly call tools/list until ctx is cancelled.
// Each client holds its own Streamable-HTTP transport (and, in stateful mode,
// its own standalone SSE GET stream), which is the concurrency that provoked
// the SSE-stall cold-start wedge (mitto-6hr). Clients run best-effort: transient
// errors are logged at DEBUG and retried.
func startMCPLoad(ctx context.Context, port, n int, logger *slog.Logger) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	fmt.Printf("🌀 Holding %d concurrent MCP client session(s) against %s\n", n, endpoint)
	for i := 0; i < n; i++ {
		go func(id int) {
			client := mcp.NewClient(&mcp.Implementation{
				Name:    fmt.Sprintf("mitto-prompt-load-%d", id),
				Version: "dev",
			}, nil)
			sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
			if err != nil {
				if logger != nil {
					logger.Debug("mcp load client connect failed", "client", id, "error", err)
				}
				return
			}
			defer sess.Close()
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := sess.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
						if logger != nil {
							logger.Debug("mcp load client list_tools failed", "client", id, "error", err)
						}
					}
				}
			}
		}(i)
	}
}
