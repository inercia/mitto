package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/reeflective/readline"
	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/acp"
)

var (
	// CLI-specific flags
	oncePrompt string
)

// cliCmd represents the cli command
var cliCmd = &cobra.Command{
	Use:   "cli",
	Short: "Interactive command-line interface for ACP communication",
	Long: `Start an interactive session with an ACP server.

This command launches the configured ACP server and provides
a readline-based interface for sending prompts and receiving
responses from the AI agent.

Use --once to send a single prompt and exit:
  mitto cli --once "What is the capital of France?"

Commands (interactive mode only):
  /quit, /exit  - Exit the CLI
  /cancel       - Cancel the current operation
  /help         - Show available commands`,
	RunE: runCLI,
}

func init() {
	rootCmd.AddCommand(cliCmd)

	// CLI-specific flags
	cliCmd.Flags().StringVar(&oncePrompt, "once", "", "Send a single prompt and exit (non-interactive mode)")
}

func runCLI(cmd *cobra.Command, args []string) error {
	server, err := getSelectedServer()
	if err != nil {
		return err
	}

	// Determine if we're in once mode (non-interactive)
	isOnceMode := oncePrompt != ""

	// Only show startup messages in interactive mode or debug mode
	if !isOnceMode || debug {
		fmt.Printf("🚀 Starting ACP server: %s\n", server.Name)
		fmt.Printf("   Command: %s\n", server.Command)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		if !isOnceMode {
			fmt.Println("\n\n👋 Shutting down...")
		}
		cancel()
	}()

	// Set up logger if debug is enabled
	var logger *slog.Logger
	if debug {
		logger = slog.Default()
	}

	// Output function for the ACP client
	output := func(msg string) {
		fmt.Print(msg)
	}

	// Create ACP connection
	conn, err := acp.NewConnection(ctx, server.Command, autoApprove, output, logger)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Initialize connection
	if err := conn.Initialize(ctx); err != nil {
		return err
	}

	// Create session
	if err := conn.NewSession(ctx, mustGetCwd()); err != nil {
		return err
	}

	// Run in once mode or interactive mode
	if isOnceMode {
		return runOnceMode(ctx, conn, oncePrompt)
	}
	return runInteractiveLoop(ctx, conn)
}

// runOnceMode sends a single prompt and exits after receiving the response.
func runOnceMode(ctx context.Context, conn *acp.Connection, prompt string) error {
	// Send the prompt to the agent
	if err := conn.Prompt(ctx, prompt); err != nil {
		return fmt.Errorf("prompt error: %w", err)
	}

	// Add a newline after the response for clean output
	fmt.Println()
	return nil
}

// slashCommands defines the available slash commands with their descriptions.
var slashCommands = []struct {
	name        string
	description string
}{
	{"/help", "Show available commands"},
	{"/h", "Show available commands (alias)"},
	{"/?", "Show available commands (alias)"},
	{"/quit", "Exit the CLI"},
	{"/exit", "Exit the CLI (alias)"},
	{"/q", "Exit the CLI (alias)"},
	{"/cancel", "Cancel the current operation"},
}

func runInteractiveLoop(ctx context.Context, conn *acp.Connection) error {
	// Create readline shell
	rl := readline.NewShell()
	rl.Prompt.Primary(func() string { return "mitto> " })

	// Set up history
	history := readline.NewInMemoryHistory()
	rl.History.Add("default", history)

	// Set up tab completion for slash commands
	rl.Completer = func(line []rune, cursor int) readline.Completions {
		return completeInput(string(line), cursor)
	}

	fmt.Println("\n📝 Type your message and press Enter. Use /help for commands. Tab completes commands.")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-conn.Done():
			return fmt.Errorf("connection closed")
		default:
		}

		line, err := rl.Readline()
		if err != nil {
			if err == io.EOF || err == readline.ErrInterrupt {
				fmt.Println("\n👋 Goodbye!")
				return nil
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for commands
		if strings.HasPrefix(line, "/") {
			if handled := handleCommand(ctx, conn, line); handled {
				continue
			}
		}

		// Send prompt to agent
		fmt.Println() // Add spacing before response
		if err := conn.Prompt(ctx, line); err != nil {
			fmt.Printf("\n❌ Error: %v\n", err)
		}
		fmt.Println() // Add spacing after response
	}
}

func handleCommand(ctx context.Context, conn *acp.Connection, line string) bool {
	cmd := strings.ToLower(strings.TrimPrefix(line, "/"))
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	switch parts[0] {
	case "quit", "exit", "q":
		fmt.Println("👋 Goodbye!")
		os.Exit(0)
	case "cancel":
		if err := conn.Cancel(ctx); err != nil {
			fmt.Printf("❌ Cancel error: %v\n", err)
		} else {
			fmt.Println("🛑 Cancelled")
		}
	case "help", "h", "?":
		printHelp()
	default:
		fmt.Printf("❓ Unknown command: %s (use /help for available commands)\n", parts[0])
	}
	return true
}

func printHelp() {
	fmt.Println(`
Available commands:
  /quit, /exit, /q  - Exit the CLI
  /cancel           - Cancel the current operation
  /help, /h, /?     - Show this help message

Tips:
  - Type your message and press Enter to send it to the agent
  - Use Ctrl+C to exit gracefully
  - Use up/down arrows for command history
  - Use Tab to autocomplete slash commands`)
}

// completeInput provides tab completion for the CLI input.
// It completes slash commands when the input starts with "/".
func completeInput(line string, cursor int) readline.Completions {
	// Get the text up to the cursor position
	if cursor > len(line) {
		cursor = len(line)
	}
	text := line[:cursor]

	// Only complete if the line starts with "/"
	if !strings.HasPrefix(text, "/") {
		return readline.Completions{}
	}

	// Find matching commands
	var matches []string
	var descriptions []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd.name, text) {
			matches = append(matches, cmd.name)
			descriptions = append(descriptions, cmd.description)
		}
	}

	if len(matches) == 0 {
		return readline.Completions{}
	}

	// Build value-description pairs for CompleteValuesDescribed
	// Format: value1, desc1, value2, desc2, ...
	pairs := make([]string, 0, len(matches)*2)
	for i, match := range matches {
		pairs = append(pairs, match, descriptions[i])
	}

	return readline.CompleteValuesDescribed(pairs...).
		Tag("commands").
		NoSpace('/') // Don't add space after completing partial command
}
