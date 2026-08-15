package acpproc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversation"
)

func TestSharedACPProcess_LoadReplayFilter_PreventsNotificationQueueOverflow(t *testing.T) {
	const replayCount = sharedACPNotificationQueueCapacity + 512
	const loadSessionID acp.SessionId = "loading-session"

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	p := &SharedACPProcess{logger: logger}
	p.beginLoadReplaySuppression(loadSessionID)

	agentToClientR, agentToClientW := io.Pipe()
	clientToAgentR, clientToAgentW := io.Pipe()
	filtered := mittoAcp.NewJSONLineFilterReaderWithFilter(agentToClientR, logger, p.filterLoadReplayNotification)
	mux := NewMultiplexClient()
	conn := acp.NewClientSideConnection(mux, clientToAgentW, filtered,
		acp.WithMaxQueuedNotifications(sharedACPNotificationQueueCapacity))
	t.Cleanup(func() {
		_ = agentToClientR.Close()
		_ = agentToClientW.Close()
		_ = clientToAgentR.Close()
		_ = clientToAgentW.Close()
	})

	var mu sync.Mutex
	var loadingSeqs, liveSeqs []int
	registerSeqCollector := func(sessionID acp.SessionId, dst *[]int) {
		mux.RegisterSession(sessionID, &conversation.SessionCallbacks{
			OnSessionUpdate: func(_ context.Context, n acp.SessionNotification) error {
				mu.Lock()
				*dst = append(*dst, int(n.Meta["seq"].(float64)))
				mu.Unlock()
				time.Sleep(time.Millisecond)
				return nil
			},
		})
	}
	registerSeqCollector(loadSessionID, &loadingSeqs)
	registerSeqCollector("live-session", &liveSeqs)

	for i := 0; i < replayCount; i++ {
		writeSessionUpdate(t, agentToClientW, loadSessionID, i)
	}
	waitForSuppressedReplayCount(t, p, loadSessionID, replayCount)
	writeSessionUpdate(t, agentToClientW, "live-session", 1)
	waitForSeqCount(t, &mu, &liveSeqs, 1)

	p.endLoadReplaySuppression(loadSessionID)
	writeSessionUpdate(t, agentToClientW, loadSessionID, 2)
	writeSessionUpdate(t, agentToClientW, loadSessionID, 3)
	waitForSeqCount(t, &mu, &loadingSeqs, 2)

	select {
	case <-conn.Done():
		t.Fatal("ACP connection closed despite load-replay suppression")
	default:
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(loadingSeqs) != "[2 3]" {
		t.Fatalf("post-load notifications = %v, want ordered [2 3]", loadingSeqs)
	}
	if fmt.Sprint(liveSeqs) != "[1]" {
		t.Fatalf("unrelated live notifications = %v, want [1]", liveSeqs)
	}
	if !strings.Contains(logBuf.String(), "would pressure ACP notification queue") ||
		!strings.Contains(logBuf.String(), "queue_capacity=8192") {
		t.Fatalf("missing pre-overflow queue-pressure log: %s", logBuf.String())
	}
}

func writeSessionUpdate(t *testing.T, w io.Writer, sessionID acp.SessionId, seq int) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  acp.ClientMethodSessionUpdate,
		"params": acp.SessionNotification{
			SessionId: sessionID,
			Meta:      map[string]any{"seq": seq},
			Update: acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.TextBlock("replay"),
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		t.Fatalf("write notification: %v", err)
	}
}

func waitForSuppressedReplayCount(t *testing.T, p *SharedACPProcess, sessionID acp.SessionId, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.loadReplayMu.RLock()
		state := p.loadReplaySuppressions[sessionID]
		got := uint64(0)
		if state != nil {
			got = state.suppressed.Load()
		}
		p.loadReplayMu.RUnlock()
		if got == uint64(want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("suppressed replay count did not reach %d", want)
}

func waitForSeqCount(t *testing.T, mu *sync.Mutex, seqs *[]int, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*seqs)
		mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("notification count did not reach %d", want)
}
