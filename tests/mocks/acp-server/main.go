// Package main implements a mock ACP server for testing Mitto.
// It communicates via stdin/stdout using JSON-RPC, implementing the ACP protocol.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	scenarioDir  string
	defaultDelay time.Duration
	verbose      bool
)

func main() {
	flag.StringVar(&scenarioDir, "scenarios", "", "Directory containing scenario JSON files")
	flag.DurationVar(&defaultDelay, "delay", 50*time.Millisecond, "Default delay between response chunks")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging to stderr")
	flag.Parse()

	// Find scenarios directory
	if scenarioDir == "" {
		// Try to find it relative to the binary or working directory
		candidates := []string{
			"tests/fixtures/responses",
			"../fixtures/responses",
			"../../fixtures/responses",
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				scenarioDir = c
				break
			}
		}
	}

	server := NewMockACPServer(scenarioDir, defaultDelay, verbose)
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// MockACPServer implements a mock ACP server
type MockACPServer struct {
	scenarios    map[string]*Scenario
	scenarioDir  string
	defaultDelay time.Duration
	verbose      bool
	sessionID    string
	initialized  bool
	reader       *bufio.Reader
	writer       io.Writer
}

// NewMockACPServer creates a new mock ACP server
func NewMockACPServer(scenarioDir string, defaultDelay time.Duration, verbose bool) *MockACPServer {
	server := &MockACPServer{
		scenarios:    make(map[string]*Scenario),
		scenarioDir:  scenarioDir,
		defaultDelay: defaultDelay,
		verbose:      verbose,
		reader:       bufio.NewReader(os.Stdin),
		writer:       os.Stdout,
	}
	server.loadScenarios()
	return server
}

func (s *MockACPServer) log(format string, args ...interface{}) {
	if s.verbose {
		fmt.Fprintf(os.Stderr, "[mock-acp] "+format+"\n", args...)
	}
}

func (s *MockACPServer) loadScenarios() {
	if s.scenarioDir == "" {
		s.log("No scenario directory specified, using default responses")
		return
	}

	files, err := filepath.Glob(filepath.Join(s.scenarioDir, "*.json"))
	if err != nil {
		s.log("Error loading scenarios: %v", err)
		return
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			s.log("Error reading %s: %v", file, err)
			continue
		}

		var scenario Scenario
		if err := json.Unmarshal(data, &scenario); err != nil {
			s.log("Error parsing %s: %v", file, err)
			continue
		}

		s.scenarios[scenario.Name] = &scenario
		s.log("Loaded scenario: %s", scenario.Name)
	}
}

// Run starts the server main loop
func (s *MockACPServer) Run() error {
	s.log("Mock ACP server starting...")

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.log("EOF received, shutting down")
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		s.log("Received: %s", line)

		if err := s.handleMessage(line); err != nil {
			s.log("Error handling message: %v", err)
		}
	}
}
