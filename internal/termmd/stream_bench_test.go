package termmd

import (
	"os"
	"path/filepath"
	"testing"
)

// readStreamCorpus loads the streaming benchmark corpus, a realistic long
// agent answer used to demonstrate (and later guard) the O(n^2) cost of
// re-rendering a whole in-flight message on every streamed chunk (mitto-
// pscc.8.1's stated prerequisite: land the benchmark first, only build the
// cache if it shows the cost).
func readStreamCorpus(b *testing.B) string {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "stream_corpus.md"))
	if err != nil {
		b.Fatalf("read stream corpus: %v", err)
	}
	return string(data)
}

// streamChunks splits body into a sequence of growing prefixes, simulating
// how a real streaming session appends a small chunk of new bytes to the
// accumulated message on every flush. chunkSize approximates a typical SSE
// text-delta size.
func streamChunks(body string, chunkSize int) []string {
	var chunks []string
	for end := chunkSize; end < len(body); end += chunkSize {
		chunks = append(chunks, body[:end])
	}
	chunks = append(chunks, body)
	return chunks
}

const streamBenchChunkSize = 40

// BenchmarkStreamingRerender_Naive replays the corpus as a sequence of
// growing prefixes through the existing whole-body Render, mirroring
// internal/chatui's current AppendOrUpdateAgent -> item.invalidate ->
// item.render behavior: every chunk re-renders the entire accumulated body.
func BenchmarkStreamingRerender_Naive(b *testing.B) {
	body := readStreamCorpus(b)
	chunks := streamChunks(body, streamBenchChunkSize)
	opts := Options{Mode: ModeStyled, Width: testWidth}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range chunks {
			_ = Render(c, opts)
		}
	}
}

// BenchmarkStreamingRerender_StablePrefix replays the same chunk sequence
// through StreamRenderer, which should only re-render the trailing partial
// once a safe boundary has been cached.
func BenchmarkStreamingRerender_StablePrefix(b *testing.B) {
	body := readStreamCorpus(b)
	chunks := streamChunks(body, streamBenchChunkSize)
	opts := Options{Mode: ModeStyled, Width: testWidth}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sr := NewStreamRenderer(opts)
		for _, c := range chunks {
			_ = sr.Render(c)
		}
	}
}
