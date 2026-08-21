package logging

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDefaultFileLogConfigCoversRepresentativeDay(t *testing.T) {
	cfg := DefaultFileLogConfig()
	const (
		observedBytes    = 40 * 1024 * 1024
		observedDuration = 3*time.Hour + 35*time.Minute
	)
	capacityBytes := int64(cfg.MaxBackups+1) * int64(cfg.MaxSizeMB) * 1024 * 1024
	estimatedCoverage := time.Duration(float64(observedDuration) * float64(capacityBytes) / observedBytes)
	if estimatedCoverage < 24*time.Hour {
		t.Errorf("estimated default coverage = %v, want at least 24h", estimatedCoverage)
	}
	if capacityBytes != 330*1024*1024 {
		t.Errorf("uncompressed disk bound = %d, want %d", capacityBytes, 330*1024*1024)
	}
}

func TestRetentionWriterTracksRotationsAndDrops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mitto.log")
	writer := newRetentionWriter(path, 1, 1, false)
	t.Cleanup(func() { _ = writer.Close() })

	payload := append(bytes.Repeat([]byte("x"), 600*1024), '\n')
	for i := 0; i < 3; i++ {
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	snapshot := writer.snapshot()
	if snapshot.Rotations != 2 {
		t.Errorf("Rotations = %d, want 2", snapshot.Rotations)
	}
	if snapshot.DroppedRotations != 1 {
		t.Errorf("DroppedRotations = %d, want 1", snapshot.DroppedRotations)
	}
	if snapshot.MaxBackups != 1 || snapshot.MaxSizeMB != 1 {
		t.Errorf("unexpected bounds: %+v", snapshot)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		backups, _, _ := inspectRetentionFiles(path)
		if backups <= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup count remained above bound: %d", backups)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRetentionWriterUnlimitedBackupsNeverDrops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mitto.log")
	writer := newRetentionWriter(path, 1, 0, false)
	t.Cleanup(func() { _ = writer.Close() })
	payload := append(bytes.Repeat([]byte("x"), 600*1024), '\n')
	for i := 0; i < 3; i++ {
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	snapshot := writer.snapshot()
	if snapshot.Rotations != 2 || snapshot.DroppedRotations != 0 {
		t.Errorf("unexpected unlimited counters: %+v", snapshot)
	}
}

func TestRetentionWriterConcurrentSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mitto.log")
	writer := newRetentionWriter(path, 1, 2, false)
	t.Cleanup(func() { _ = writer.Close() })

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := writer.Write([]byte("time=2026-08-13T20:00:00Z level=INFO msg=concurrent\n")); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = writer.snapshot()
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Write: %v", err)
	}
	if snapshot := writer.snapshot(); snapshot.RetainedFiles == 0 || snapshot.RetainedBytes == 0 {
		t.Errorf("unexpected final snapshot: %+v", snapshot)
	}
}

func TestInspectRetentionFilesReadsCompressedOldestRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mitto.log")
	if err := os.WriteFile(path, []byte("time=2026-08-13T20:00:00Z level=INFO msg=current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "mitto-2026-08-13T19-00-00.000.log.gz")
	file, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	if _, err := gz.Write([]byte("time=2026-08-13T18:30:00Z level=INFO msg=oldest\n")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	backups, retainedBytes, oldest := inspectRetentionFiles(path)
	if backups != 1 {
		t.Errorf("backups = %d, want 1", backups)
	}
	if retainedBytes <= 0 {
		t.Errorf("retainedBytes = %d, want > 0", retainedBytes)
	}
	want := time.Date(2026, 8, 13, 18, 30, 0, 0, time.UTC)
	if !oldest.Equal(want) {
		t.Errorf("oldest = %v, want %v", oldest, want)
	}
}

func TestCurrentFileRetentionLifecycle(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	path := filepath.Join(t.TempDir(), "mitto.log")
	if err := Initialize(Config{Level: "info", FileLog: &FileLogConfig{
		Path: path, MaxSizeMB: 2, MaxBackups: 4, Compress: true,
	}}); err != nil {
		t.Fatal(err)
	}
	Get().Info("retention lifecycle")

	snapshot, ok := CurrentFileRetention()
	if !ok {
		t.Fatal("CurrentFileRetention returned no active snapshot")
	}
	if snapshot.Path != path || snapshot.RetainedFiles != 1 || snapshot.RetainedBytes == 0 {
		t.Errorf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.CounterStartedAt.IsZero() || snapshot.OldestRetainedAt.IsZero() {
		t.Errorf("missing timestamps: %+v", snapshot)
	}

	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CurrentFileRetention(); ok {
		t.Error("snapshot remained active after Close")
	}
}

func TestCurrentFileRetentionDisabledForConsoleAndLegacyFiles(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	if err := Initialize(Config{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := CurrentFileRetention(); ok {
		t.Error("console-only logging reported retention")
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.log")
	if err := Initialize(Config{Level: "info", LogFile: legacyPath}); err != nil {
		t.Fatal(err)
	}
	Get().Info("legacy logger")
	if _, ok := CurrentFileRetention(); ok {
		t.Error("legacy unrotated logging reported retention")
	}
}
