---
description: Go integration tests, mock ACP server, in-process testing, and httptest patterns
globs:
  - "tests/integration/**/*"
  - "tests/mocks/**/*"
  - "internal/client/**/*"
keywords:
  - integration test
  - mock ACP
  - httptest
  - in-process
  - SetupTestServer
---

# Integration Testing

## Test Directory Structure

```
tests/
├── fixtures/                    # Shared test fixtures
├── mocks/
│   ├── acp-server/              # Mock ACP server (Go)
│   └── testutil/                # Shared Go test utilities
├── integration/
│   ├── cli/                     # CLI command tests (external process)
│   ├── api/                     # HTTP/WebSocket API tests (external process)
│   └── inprocess/               # In-process integration tests (fast)
└── scripts/                     # Test support scripts
```

## Running Integration Tests

```bash
make test-integration      # All integration tests
make test-integration-cli  # CLI tests only
make test-integration-api  # API tests only
```

## Mock ACP Server

The mock server (`tests/mocks/acp-server/`) provides deterministic responses:

```bash
make build-mock-acp  # Build the mock server
./tests/mocks/acp-server/mock-acp-server --verbose  # Run manually
```

## Finding Project Root in Tests

```go
func getMockACPPath(t *testing.T) string {
    t.Helper()
    dir, _ := os.Getwd()
    for {
        if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
            break
        }
        parent := filepath.Dir(dir)
        if parent == dir { t.Skip("Could not find project root") }
        dir = parent
    }
    mockPath := filepath.Join(dir, "tests", "mocks", "acp-server", "mock-acp-server")
    if _, err := os.Stat(mockPath); os.IsNotExist(err) {
        t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first.")
    }
    return mockPath
}
```

## In-Process Integration Tests

The `tests/integration/inprocess/` package runs the web server in-process using `httptest.Server`:

| Aspect | In-Process | External Process |
|--------|------------|------------------|
| Speed | Fast (no process spawn) | Slower |
| Debugging | Easy (same process) | Harder |
| Coverage | Counted in reports | Not counted |

### Test Server Setup

```go
func SetupTestServer(t *testing.T) *TestServer {
    t.Helper()
    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)
    appdir.ResetCache()
    t.Cleanup(appdir.ResetCache)

    mockACPCmd := findMockACPServer(t)
    webConfig := web.Config{
        ACPCommand:        mockACPCmd,
        ACPServer:         "mock-acp",
        DefaultWorkingDir: filepath.Join(tmpDir, "workspace"),
        AutoApprove:       true,
    }
    srv, _ := web.NewServer(webConfig)
    httpServer := httptest.NewServer(srv.Handler())
    t.Cleanup(httpServer.Close)

    return &TestServer{
        Server:     srv,
        HTTPServer: httpServer,
        Client:     client.New(httpServer.URL),
    }
}
```

### Using the Go Client

```go
func TestSessionLifecycle(t *testing.T) {
    ts := SetupTestServer(t)

    session, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "Test"})
    if err != nil { t.Fatalf("CreateSession failed: %v", err) }
    defer ts.Client.DeleteSession(session.SessionID)

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    ws, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
        OnAgentMessage: func(html string) { t.Logf("Agent: %s", html) },
    })
    if err != nil { t.Fatalf("Connect failed: %v", err) }
    defer ws.Close()

    ws.SendPrompt("Hello, test!")
}
```

## WebSocket Integration Testing

```go
func TestWebSocketMessageFlow(t *testing.T) {
    mux := http.NewServeMux()
    upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

    mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        conn, _ := upgrader.Upgrade(w, r, nil)
        defer conn.Close()
        // Handle messages...
    })

    server := httptest.NewServer(mux)
    defer server.Close()

    wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
    conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
    defer conn.Close()

    conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"prompt"}`))
    _, msg, _ := conn.ReadMessage()
    // Assert on msg...
}
```

## Key Test Scenarios

| Test | What It Validates |
|------|-------------------|
| `TestSessionLifecycle` | Create → List → Get → Delete |
| `TestQueueOperations` | Add → List → Clear queue |
| `TestWebSocketConnection` | Connect → Callbacks → Disconnect |
| `TestSendPromptAndReceiveResponse` | Full prompt/response flow |

## Running In-Process Tests

```bash
go test -tags integration -v ./tests/integration/inprocess

# With coverage
go test -tags integration -coverprofile=coverage.out \
    -coverpkg=./internal/web/...,./internal/client/... \
    ./tests/integration/inprocess
```

