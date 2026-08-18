package bdexec

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestAcquireLogsWaitAndExecutionDurationsSeparately(t *testing.T) {
	var logBuf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(original) })

	release, err := Acquire(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	time.Sleep(time.Millisecond)
	release()

	output := logBuf.String()
	for _, want := range []string{
		"bd limiter slot released",
		"bd_database=",
		"bd_limiter_wait_ms=",
		"bd_database_wait_ms=",
		"bd_global_wait_ms=",
		"bd_execution_ms=",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("log output missing %q: %s", want, output)
		}
	}
}
