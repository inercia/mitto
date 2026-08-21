package logging

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const lumberjackBackupTimeFormat = "2006-01-02T15-04-05.000"

// FileRetentionSnapshot describes the active rotated file logger. Rotation and
// drop counters are process-local; retained-file coverage is read from disk.
type FileRetentionSnapshot struct {
	Path                string
	MaxSizeMB           int
	MaxBackups          int
	Compress            bool
	RetainedFiles       int
	RetainedBytes       int64
	OldestRetainedAt    time.Time
	RetainedSpanSeconds int64
	Rotations           uint64
	DroppedRotations    uint64
	CounterStartedAt    time.Time
}

type retentionWriter struct {
	mu              sync.Mutex
	writer          *lumberjack.Logger
	path            string
	maxSizeBytes    int64
	maxBackups      int
	compress        bool
	currentBytes    int64
	currentExists   bool
	opened          bool
	retainedBackups int
	rotations       uint64
	dropped         uint64
	counterStarted  time.Time
}

func newRetentionWriter(path string, maxSizeMB, maxBackups int, compress bool) *retentionWriter {
	artifacts, _, _ := inspectRetentionFiles(path)
	w := &retentionWriter{
		writer: &lumberjack.Logger{
			Filename: path, MaxSize: maxSizeMB, MaxBackups: maxBackups, Compress: compress,
		},
		path: path, maxSizeBytes: int64(maxSizeMB) * 1024 * 1024,
		maxBackups: maxBackups, compress: compress,
		retainedBackups: artifacts, counterStarted: time.Now(),
	}
	if info, err := os.Stat(path); err == nil {
		w.currentBytes = info.Size()
		w.currentExists = true
	}
	return w
}

func (w *retentionWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	writeLen := int64(len(p))
	willRotate := w.currentExists && ((!w.opened && w.currentBytes+writeLen >= w.maxSizeBytes) ||
		(w.opened && w.currentBytes+writeLen > w.maxSizeBytes))
	n, err := w.writer.Write(p)
	if err == nil || n > 0 {
		if willRotate {
			w.recordRotation()
			w.currentBytes = int64(n)
		} else {
			w.currentBytes += int64(n)
		}
		w.currentExists = true
		w.opened = true
	}
	return n, err
}

func (w *retentionWriter) recordRotation() {
	w.rotations++
	next := w.retainedBackups + 1
	if w.maxBackups > 0 && next > w.maxBackups {
		w.dropped += uint64(next - w.maxBackups)
		w.retainedBackups = w.maxBackups
		return
	}
	w.retainedBackups = next
}

func (w *retentionWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = false
	return w.writer.Close()
}

func (w *retentionWriter) snapshot() FileRetentionSnapshot {
	w.mu.Lock()
	snapshot := FileRetentionSnapshot{
		Path: w.path, MaxSizeMB: int(w.maxSizeBytes / (1024 * 1024)),
		MaxBackups: w.maxBackups, Compress: w.compress,
		Rotations: w.rotations, DroppedRotations: w.dropped, CounterStartedAt: w.counterStarted,
	}
	w.mu.Unlock()

	backups, bytes, oldest := inspectRetentionFiles(snapshot.Path)
	if info, err := os.Stat(snapshot.Path); err == nil {
		snapshot.RetainedFiles = backups + 1
		snapshot.RetainedBytes = bytes + info.Size()
		if oldest.IsZero() {
			oldest = firstLogRecordTime(snapshot.Path)
			if oldest.IsZero() {
				oldest = info.ModTime()
			}
		}
	} else {
		snapshot.RetainedFiles = backups
		snapshot.RetainedBytes = bytes
	}
	snapshot.OldestRetainedAt = oldest
	if !oldest.IsZero() {
		snapshot.RetainedSpanSeconds = max(0, int64(time.Since(oldest).Seconds()))
	}
	return snapshot
}

// CurrentFileRetention returns the active rotated file logger's retention
// snapshot. The boolean is false for console-only and legacy unrotated logging.
func CurrentFileRetention() (FileRetentionSnapshot, bool) {
	logWriterMu.Lock()
	w, ok := logWriter.(*retentionWriter)
	logWriterMu.Unlock()
	if !ok {
		return FileRetentionSnapshot{}, false
	}
	return w.snapshot(), true
}

type retentionArtifact struct {
	path      string
	rotatedAt time.Time
}

func inspectRetentionFiles(path string) (int, int64, time.Time) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext) + "-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, time.Time{}
	}

	unique := make(map[string]retentionArtifact)
	var bytes int64
	for _, entry := range entries {
		name := entry.Name()
		plain := strings.TrimSuffix(name, ".gz")
		if !strings.HasPrefix(plain, prefix) || !strings.HasSuffix(plain, ext) {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(plain, prefix), ext)
		rotatedAt, parseErr := time.Parse(lumberjackBackupTimeFormat, stamp)
		if parseErr != nil {
			continue
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			bytes += info.Size()
		}
		artifact := retentionArtifact{path: filepath.Join(dir, name), rotatedAt: rotatedAt}
		if _, exists := unique[stamp]; !exists || strings.HasSuffix(artifact.path, ".gz") {
			unique[stamp] = artifact
		}
	}
	artifacts := make([]retentionArtifact, 0, len(unique))
	for _, artifact := range unique {
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].rotatedAt.Before(artifacts[j].rotatedAt) })
	if len(artifacts) == 0 {
		return 0, bytes, time.Time{}
	}
	oldest := firstLogRecordTime(artifacts[0].path)
	if oldest.IsZero() {
		oldest = artifacts[0].rotatedAt
	}
	return len(artifacts), bytes, oldest
}

func firstLogRecordTime(path string) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gz, gzErr := gzip.NewReader(file)
		if gzErr != nil {
			return time.Time{}
		}
		defer gz.Close()
		reader = gz
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "{") {
			var record struct {
				Time time.Time `json:"time"`
			}
			if json.Unmarshal([]byte(line), &record) == nil && !record.Time.IsZero() {
				return record.Time
			}
		}
		if pos := strings.Index(line, "time="); pos >= 0 {
			fields := strings.Fields(line[pos+len("time="):])
			if len(fields) == 0 {
				continue
			}
			value := strings.Trim(fields[0], `"`)
			if parsed, parseErr := time.Parse(time.RFC3339Nano, value); parseErr == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}
