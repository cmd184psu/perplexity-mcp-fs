package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ── helpers ────────────────────────────────────────────────────────────────────

func benchRoot(b *testing.B) string {
	b.Helper()
	tmp := b.TempDir()
	prev := append([]string{}, allowedRoots...)
	if err := setRoots([]string{tmp}); err != nil {
		b.Fatalf("setRoots: %v", err)
	}
	b.Cleanup(func() { allowedRoots = prev })
	return tmp
}

func benchCall(b *testing.B, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	b.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		b.Fatalf("handler error: %v", err)
	}
	return res
}

// ── read_file benchmarks ────────────────────────────────────────────────────────

func BenchmarkReadFileSmall(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "small.txt")
	os.WriteFile(p, []byte("hello world"), 0644)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, handleReadFile, map[string]any{"path": p})
	}
}

func BenchmarkReadFileMedium(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "medium.txt")
	os.WriteFile(p, make([]byte, 64*1024), 0644) // 64 KB
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, handleReadFile, map[string]any{"path": p})
	}
}

func BenchmarkReadFileLarge(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "large.txt")
	os.WriteFile(p, make([]byte, 1024*1024), 0644) // 1 MB
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, handleReadFile, map[string]any{"path": p})
	}
}

// ── write_file benchmarks ───────────────────────────────────────────────────────

func BenchmarkWriteFileSmall(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "out.txt")
	content := "hello world"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, handleWriteFile, map[string]any{"path": p, "content": content})
	}
}

func BenchmarkWriteFileMedium(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "out.txt")
	content := string(make([]byte, 64*1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchCall(b, handleWriteFile, map[string]any{"path": p, "content": content})
	}
}

// ── patch_file benchmark ────────────────────────────────────────────────────────

func BenchmarkPatchFile(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "patch.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		os.WriteFile(p, []byte("REPLACE_ME some content after"), 0644)
		b.StartTimer()
		benchCall(b, handlePatchFile, map[string]any{"path": p, "old_str": "REPLACE_ME", "new_str": "DONE"})
	}
}

// ── concurrency benchmarks ─────────────────────────────────────────────────────

// BenchmarkConcurrentReads fires N parallel read_file calls on the same file.
// This is the primary stress test for the gate + worker pool.
func BenchmarkConcurrentReadsSameFile(b *testing.B) {
	root := benchRoot(b)
	p := filepath.Join(root, "shared.txt")
	os.WriteFile(p, []byte("shared content for concurrent reads"), 0644)

	b.SetParallelism(16)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchCall(b, handleReadFile, map[string]any{"path": p})
		}
	})
}

func BenchmarkConcurrentReadsDifferentFiles(b *testing.B) {
	root := benchRoot(b)
	const nFiles = 50
	for i := 0; i < nFiles; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d.txt", i)), []byte("content"), 0644)
	}

	var idx atomic.Int64
	b.SetParallelism(16)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(idx.Add(1)) % nFiles
			p := filepath.Join(root, fmt.Sprintf("f%d.txt", i))
			benchCall(b, handleReadFile, map[string]any{"path": p})
		}
	})
}

func BenchmarkConcurrentWritesDifferentFiles(b *testing.B) {
	root := benchRoot(b)
	const nFiles = 20
	var idx atomic.Int64
	b.SetParallelism(8)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(idx.Add(1)) % nFiles
			p := filepath.Join(root, fmt.Sprintf("w%d.txt", i))
			benchCall(b, handleWriteFile, map[string]any{"path": p, "content": "bench"})
		}
	})
}

// BenchmarkMixedReadWrite simulates real Perplexity usage: mostly reads,
// occasional writes on the same and different paths.
func BenchmarkMixedReadWrite(b *testing.B) {
	root := benchRoot(b)
	const nFiles = 10
	for i := 0; i < nFiles; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("m%d.txt", i)), []byte("initial content for file"), 0644)
	}

	var opIdx atomic.Int64
	b.SetParallelism(12)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			op := opIdx.Add(1)
			i := int(op) % nFiles
			p := filepath.Join(root, fmt.Sprintf("m%d.txt", i))
			if op%5 == 0 {
				// 1-in-5 is a write
				benchCall(b, handleWriteFile, map[string]any{"path": p, "content": fmt.Sprintf("updated %d", op)})
			} else {
				benchCall(b, handleReadFile, map[string]any{"path": p})
			}
		}
	})
}

// ── latency percentile test (not a benchmark, but exercises timing) ──────────

// TestReadFileLatencyPercentiles runs 200 serial reads and asserts p99 < 10ms.
// On an M4 with NVMe a local read should complete in <1ms; 10ms is very generous.
func TestReadFileLatencyPercentiles(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "latency.txt")
	os.WriteFile(p, []byte("latency test content for a typical small file used by MCP"), 0644)

	const n = 200
	latencies := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		res := callTool(t, handleReadFile, map[string]any{"path": p})
		latencies[i] = time.Since(start)
		if isError(res) {
			t.Fatalf("read %d failed: %s", i, resultText(res))
		}
	}

	// Sort to compute percentiles inline.
	for i := 1; i < n; i++ {
		for j := i; j > 0 && latencies[j] < latencies[j-1]; j-- {
			latencies[j], latencies[j-1] = latencies[j-1], latencies[j]
		}
	}
	p50 := latencies[n/2]
	p95 := latencies[int(n*95/100)]
	p99 := latencies[int(n*99/100)]
	pMax := latencies[n-1]

	t.Logf("read_file latency over %d iterations: p50=%v p95=%v p99=%v max=%v", n, p50, p95, p99, pMax)

	const maxP99 = 10 * time.Millisecond
	if p99 > maxP99 {
		t.Errorf("p99 latency %v exceeds threshold %v — performance regression", p99, maxP99)
	}
}

// TestWriteFileLatencyPercentiles same treatment for write_file.
func TestWriteFileLatencyPercentiles(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "latency-write.txt")

	const n = 200
	latencies := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		res := callTool(t, handleWriteFile, map[string]any{"path": p, "content": "latency write test"})
		latencies[i] = time.Since(start)
		if isError(res) {
			t.Fatalf("write %d failed: %s", i, resultText(res))
		}
	}

	for i := 1; i < n; i++ {
		for j := i; j > 0 && latencies[j] < latencies[j-1]; j-- {
			latencies[j], latencies[j-1] = latencies[j-1], latencies[j]
		}
	}
	p50 := latencies[n/2]
	p95 := latencies[int(n*95/100)]
	p99 := latencies[int(n*99/100)]
	pMax := latencies[n-1]

	t.Logf("write_file latency over %d iterations: p50=%v p95=%v p99=%v max=%v", n, p50, p95, p99, pMax)

	const maxP99 = 10 * time.Millisecond
	if p99 > maxP99 {
		t.Errorf("p99 latency %v exceeds threshold %v — performance regression", p99, maxP99)
	}
}

// TestConcurrentReadWriteNoDeadlock fires 50 goroutines doing interleaved
// reads and writes on 5 shared files for 2 seconds.  Deadlock = test hangs.
func TestConcurrentReadWriteNoDeadlock(t *testing.T) {
	root := setupRoot(t)
	const nFiles = 5
	for i := 0; i < nFiles; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("dl%d.txt", i)), []byte("init"), 0644)
	}

	deadline := time.Now().Add(2 * time.Second)
	var wg sync.WaitGroup
	var ops atomic.Int64
	errCh := make(chan string, 50)

	for g := 0; g < 50; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				i := g % nFiles
				p := filepath.Join(root, fmt.Sprintf("dl%d.txt", i))
				var handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
				var args map[string]any
				if g%3 == 0 {
					handler = handleWriteFile
					args = map[string]any{"path": p, "content": fmt.Sprintf("w%d", g)}
				} else {
					handler = handleReadFile
					args = map[string]any{"path": p}
				}
				req := mcp.CallToolRequest{}
				req.Params.Arguments = args
				res, err := handler(context.Background(), req)
				if err != nil {
					select {
					case errCh <- fmt.Sprintf("goroutine %d: handler error: %v", g, err):
					default:
					}
					return
				}
				if res.IsError {
					select {
					case errCh <- fmt.Sprintf("goroutine %d: tool error: %v", g, res.Content):
					default:
					}
					return
				}
				ops.Add(1)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		select {
		case msg := <-errCh:
			t.Fatalf("goroutine reported error: %s", msg)
		default:
		}
		t.Logf("completed %d ops in 2s with no deadlock", ops.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: goroutines did not complete within 5s")
	}
}
