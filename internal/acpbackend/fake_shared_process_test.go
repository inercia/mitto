package acpbackend

import (
	"context"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/conversation"
)

// fakeSharedProcess is a minimal conversation.SharedProcess test double.
// Mirrors the pattern used by internal/conversation's own fakeSharedProcess
// (shared_session_handshaker_test.go) so acpbackend's tests exercise the
// adapter against a realistic double rather than a type-asserted concrete
// SharedACPProcess.
type fakeSharedProcess struct {
	mu sync.Mutex

	caps *acp.AgentCapabilities

	newSessionHandle *conversation.SessionHandle
	newSessionErr    error

	loadSessionHandle *conversation.SessionHandle
	loadSessionErr    error

	resumeSessionHandle *conversation.SessionHandle
	resumeSessionErr    error

	registered   map[acp.SessionId]*conversation.SessionCallbacks
	unregistered []acp.SessionId

	promptResp acp.PromptResponse
	promptErr  error
	promptArgs []acp.ContentBlock

	cancelErr    error
	cancelCalls  []acp.SessionId
	setModeErr   error
	setModeCalls []struct {
		id   acp.SessionId
		mode string
	}
	setModelErr   error
	setModelCalls []struct {
		id    acp.SessionId
		model string
	}

	done chan struct{}
}

func newFakeSharedProcess() *fakeSharedProcess {
	return &fakeSharedProcess{
		caps:       &acp.AgentCapabilities{},
		registered: make(map[acp.SessionId]*conversation.SessionCallbacks),
		done:       make(chan struct{}),
	}
}

func (f *fakeSharedProcess) NewSession(_ context.Context, _ string, _ []acp.McpServer) (*conversation.SessionHandle, error) {
	return f.newSessionHandle, f.newSessionErr
}

func (f *fakeSharedProcess) LoadSession(_ context.Context, _, _ string, _ []acp.McpServer) (*conversation.SessionHandle, error) {
	return f.loadSessionHandle, f.loadSessionErr
}

func (f *fakeSharedProcess) ResumeSession(_ context.Context, _, _ string, _ []acp.McpServer) (*conversation.SessionHandle, error) {
	return f.resumeSessionHandle, f.resumeSessionErr
}

func (f *fakeSharedProcess) RegisterSession(sessionID acp.SessionId, callbacks *conversation.SessionCallbacks) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered[sessionID] = callbacks
}

func (f *fakeSharedProcess) UnregisterSession(sessionID acp.SessionId) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.registered, sessionID)
	f.unregistered = append(f.unregistered, sessionID)
}

func (f *fakeSharedProcess) ProcessDone() <-chan struct{} { return f.done }

func (f *fakeSharedProcess) Prompt(_ context.Context, _ acp.SessionId, content []acp.ContentBlock) (acp.PromptResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptArgs = content
	return f.promptResp, f.promptErr
}

func (f *fakeSharedProcess) Cancel(_ context.Context, sessionID acp.SessionId) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls = append(f.cancelCalls, sessionID)
	return f.cancelErr
}

func (f *fakeSharedProcess) SetSessionMode(_ context.Context, sessionID acp.SessionId, modeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModeCalls = append(f.setModeCalls, struct {
		id   acp.SessionId
		mode string
	}{sessionID, modeID})
	return f.setModeErr
}

func (f *fakeSharedProcess) SetSessionModel(_ context.Context, sessionID acp.SessionId, modelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModelCalls = append(f.setModelCalls, struct {
		id    acp.SessionId
		model string
	}{sessionID, modelID})
	return f.setModelErr
}

func (f *fakeSharedProcess) Done() <-chan struct{}                       { return f.done }
func (f *fakeSharedProcess) Capabilities() *acp.AgentCapabilities        { return f.caps }
func (f *fakeSharedProcess) Generation() int                             { return 0 }
func (f *fakeSharedProcess) Restart(_ int) error                         { return nil }
func (f *fakeSharedProcess) RecommendedLoadTimeout(_ bool) time.Duration { return 0 }
func (f *fakeSharedProcess) MCPInitDone() bool                           { return true }
func (f *fakeSharedProcess) WaitForMCPInit(_ context.Context) bool       { return true }

var _ conversation.SharedProcess = (*fakeSharedProcess)(nil)
