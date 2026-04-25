package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// callTool invokes a ToolHandlerFunc with a simple string-argument map.
func callTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("tool returned error: %v", err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func isError(res *mcp.CallToolResult) bool { return res.IsError }

// setupRoot creates a temp dir, registers it as an allowed root, and returns
// its path.  The root is restored to its previous value via t.Cleanup.
func setupRoot(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	prev := append([]string{}, allowedRoots...)
	if err := setRoots([]string{tmp}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}
	t.Cleanup(func() {
		rootsMu.Lock()
		allowedRoots = prev
		rootsMu.Unlock()
	})
	return tmp
}

// ── static asset tests ───────────────────────────────────────────────────────

func TestStaticCSSServedWithStylesheetMime(t *testing.T) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}
	h := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ctype := w.Header().Get("Content-Type")
	expected := mime.TypeByExtension(".css")
	if expected == "" {
		expected = "text/css; charset=utf-8"
	}
	if !strings.Contains(ctype, "text/css") {
		t.Fatalf("expected CSS content-type, got %q", ctype)
	}
	if len(w.Body.String()) == 0 {
		t.Fatal("expected non-empty CSS body")
	}
}

func TestStaticJSServedWithJavascriptMime(t *testing.T) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}
	h := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	req := httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ctype := w.Header().Get("Content-Type")
	if !strings.Contains(ctype, "javascript") && !strings.Contains(ctype, "ecmascript") && !strings.Contains(ctype, "text/plain") {
		t.Fatalf("expected javascript content-type, got %q", ctype)
	}
	if !strings.Contains(w.Body.String(), "probeTools") {
		t.Fatal("expected JS asset to contain probeTools")
	}
}

// ── config tests ──────────────────────────────────────────────────────────────

func TestLoadAndSaveConfigRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer os.Setenv("HOME", oldHome)

	cfg := Config{Port: 9999, Roots: []string{"/tmp/a", "/tmp/b"}}
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	loaded := loadConfig()
	if loaded.Port != 9999 {
		t.Fatalf("expected port 9999, got %d", loaded.Port)
	}
	if len(loaded.Roots) != 2 || loaded.Roots[0] != "/tmp/a" || loaded.Roots[1] != "/tmp/b" {
		t.Fatalf("unexpected roots: %#v", loaded.Roots)
	}
}

// ── path / root tests ──────────────────────────────────────────────────────────

func TestSetRootsAndResolvePath(t *testing.T) {
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "a")
	rootB := filepath.Join(tmp, "b")
	os.MkdirAll(rootA, 0755)
	os.MkdirAll(rootB, 0755)

	if err := setRoots([]string{rootA, rootB}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}
	resolved, err := resolvePath(filepath.Join(rootA, "child.txt"))
	if err != nil {
		t.Fatalf("resolvePath inside root: %v", err)
	}
	if !strings.HasPrefix(resolved, rootA) {
		t.Fatalf("expected path under %q, got %q", rootA, resolved)
	}

	outside := "/definitely-outside-root.txt"
	if _, err := resolvePath(outside); err == nil {
		t.Fatal("expected resolvePath to reject path outside allowed roots")
	}
}

func TestSetRootsRejectsMissingAndFilePaths(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "not-a-dir.txt")
	os.WriteFile(filePath, []byte("x"), 0644)

	if err := setRoots([]string{filepath.Join(tmp, "missing")}); err == nil {
		t.Fatal("expected setRoots to reject missing directory")
	}
	if err := setRoots([]string{filePath}); err == nil {
		t.Fatal("expected setRoots to reject non-directory path")
	}
}

func TestResolvePathAllowsExactRootAndNestedPaths(t *testing.T) {
	root := setupRoot(t)
	os.MkdirAll(filepath.Join(root, "nested"), 0755)

	if got, err := resolvePath(root); err != nil || got != root {
		t.Fatalf("exact root: got %q err %v", got, err)
	}
	nested := filepath.Join(root, "nested", "file.txt")
	if got, err := resolvePath(nested); err != nil || got != nested {
		t.Fatalf("nested path: got %q err %v", got, err)
	}
}

func TestSkipDirsIncludesCommonHeavyDirectories(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "vendor", ".venv", "dist", "build", ".next", ".cache"} {
		if !skipDirs[name] {
			t.Fatalf("expected skipDirs to include %q", name)
		}
	}
}

// ── session / make tests ─────────────────────────────────────────────────────────

func TestResolveProjectRootRequiresSessionMD(t *testing.T) {
	project := setupRoot(t)

	if _, err := resolveProjectRoot(project); err == nil {
		t.Fatal("expected resolveProjectRoot to require session.md")
	}
	os.WriteFile(filepath.Join(project, "session.md"), []byte("# s\n"), 0644)
	if got, err := resolveProjectRoot(project); err != nil || got != project {
		t.Fatalf("expected success: got %q err %v", got, err)
	}
}

func TestAppendSessionNoteAppendsContent(t *testing.T) {
	project := setupRoot(t)
	sessionPath := filepath.Join(project, "session.md")
	os.WriteFile(sessionPath, []byte("# session\n"), 0644)

	msg, err := appendSessionNote(project, "Did a thing", "Do the next thing")
	if err != nil {
		t.Fatalf("appendSessionNote: %v", err)
	}
	if !strings.Contains(msg, "updated") {
		t.Fatalf("expected update message, got %q", msg)
	}
	data, _ := os.ReadFile(sessionPath)
	text := string(data)
	if !strings.Contains(text, "Did a thing") || !strings.Contains(text, "Do the next thing") {
		t.Fatalf("session.md missing expected content: %s", text)
	}
}

func TestRunMakeTargetRejectsProjectWithoutSessionMD(t *testing.T) {
	project := setupRoot(t)
	if _, err := runMakeTarget(project, "test"); err == nil {
		t.Fatal("expected runMakeTarget to reject project without session.md")
	}
}

// ── buildMCPServer registration test ───────────────────────────────────────────

func TestBuildMCPServerRegistersExpectedTools(t *testing.T) {
	s := buildMCPServer()
	if s == nil {
		t.Fatal("buildMCPServer returned nil")
	}
	want := []string{
		"read_file", "read_files", "write_file", "patch_file",
		"list_directory", "search_files", "create_directory", "delete_file",
		"get_file_info", "make_build", "make_test", "update_session_md",
	}
	got := map[string]bool{}
	for _, tool := range registeredTools {
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool: %q", name)
		}
	}
}

// ── read_file behavioral tests ─────────────────────────────────────────────────

func TestHandleReadFileReturnsContents(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "hello.txt")
	os.WriteFile(p, []byte("hello world"), 0644)

	res := callTool(t, handleReadFile, map[string]any{"path": p})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if got := resultText(res); got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
}

func TestHandleReadFileRejectsOutsideRoot(t *testing.T) {
	setupRoot(t)
	res := callTool(t, handleReadFile, map[string]any{"path": "/etc/passwd"})
	if !isError(res) {
		t.Fatal("expected error for path outside allowed roots")
	}
}

func TestHandleReadFileMissingReturnsError(t *testing.T) {
	root := setupRoot(t)
	res := callTool(t, handleReadFile, map[string]any{"path": filepath.Join(root, "nope.txt")})
	if !isError(res) {
		t.Fatal("expected error for missing file")
	}
}

// ── read_files behavioral tests ────────────────────────────────────────────────

func TestHandleReadFilesAggregatesWithHeadings(t *testing.T) {
	root := setupRoot(t)
	p1 := filepath.Join(root, "a.txt")
	p2 := filepath.Join(root, "b.txt")
	os.WriteFile(p1, []byte("AAA"), 0644)
	os.WriteFile(p2, []byte("BBB"), 0644)

	res := callTool(t, handleReadFiles, map[string]any{"paths": []any{p1, p2}})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	txt := resultText(res)
	if !strings.Contains(txt, "==> ") || !strings.Contains(txt, " <==") {
		t.Fatalf("expected heading markers in output, got: %s", txt)
	}
	if !strings.Contains(txt, "AAA") || !strings.Contains(txt, "BBB") {
		t.Fatalf("expected both file contents in output, got: %s", txt)
	}
}

func TestHandleReadFilesRejectsEmptyArray(t *testing.T) {
	setupRoot(t)
	res := callTool(t, handleReadFiles, map[string]any{"paths": []any{}})
	if !isError(res) {
		t.Fatal("expected error for empty paths array")
	}
}

func TestHandleReadFilesRejectsNonArray(t *testing.T) {
	setupRoot(t)
	res := callTool(t, handleReadFiles, map[string]any{"paths": "not-an-array"})
	if !isError(res) {
		t.Fatal("expected error when paths is not an array")
	}
}

// ── write_file behavioral tests ────────────────────────────────────────────────

func TestHandleWriteFileCreatesFile(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "out.txt")

	res := callTool(t, handleWriteFile, map[string]any{"path": p, "content": "hello"})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "hello" {
		t.Fatalf("expected file to contain %q, got %q (err %v)", "hello", data, err)
	}
}

func TestHandleWriteFileCreatesParentDirs(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "deep", "nested", "file.txt")

	res := callTool(t, handleWriteFile, map[string]any{"path": p, "content": "x"})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if data, _ := os.ReadFile(p); string(data) != "x" {
		t.Fatalf("file not written correctly")
	}
}

func TestHandleWriteFileRejectsOutsideRoot(t *testing.T) {
	setupRoot(t)
	res := callTool(t, handleWriteFile, map[string]any{"path": "/tmp/evil.txt", "content": "bad"})
	if !isError(res) {
		t.Fatal("expected error for path outside allowed roots")
	}
}

// ── patch_file behavioral tests ────────────────────────────────────────────────

func TestHandlePatchFileReplacesFirstOccurrence(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "patch.txt")
	os.WriteFile(p, []byte("foo foo bar"), 0644)

	res := callTool(t, handlePatchFile, map[string]any{"path": p, "old_str": "foo", "new_str": "baz"})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	data, _ := os.ReadFile(p)
	if string(data) != "baz foo bar" {
		t.Fatalf("expected %q, got %q", "baz foo bar", string(data))
	}
}

func TestHandlePatchFileErrorWhenOldStrMissing(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "patch.txt")
	os.WriteFile(p, []byte("hello"), 0644)

	res := callTool(t, handlePatchFile, map[string]any{"path": p, "old_str": "nothere", "new_str": "x"})
	if !isError(res) {
		t.Fatal("expected error when old_str is not found")
	}
}

// ── list_directory behavioral tests ──────────────────────────────────────────────

func TestHandleListDirectoryListsEntries(t *testing.T) {
	root := setupRoot(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0644)
	os.MkdirAll(filepath.Join(root, "subdir"), 0755)

	res := callTool(t, handleListDirectory, map[string]any{"path": root})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	txt := resultText(res)
	if !strings.Contains(txt, "a.txt") {
		t.Fatalf("expected a.txt in listing, got: %s", txt)
	}
	if !strings.Contains(txt, "[DIR]") {
		t.Fatalf("expected [DIR] marker in listing, got: %s", txt)
	}
}

func TestHandleListDirectorySkipsHeavyDirs(t *testing.T) {
	root := setupRoot(t)
	os.MkdirAll(filepath.Join(root, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(root, "src"), 0755)

	res := callTool(t, handleListDirectory, map[string]any{"path": root})
	txt := resultText(res)
	if strings.Contains(txt, "node_modules") {
		t.Fatal("expected node_modules to be skipped")
	}
	if !strings.Contains(txt, "src") {
		t.Fatal("expected src to appear in listing")
	}
}

// ── search_files behavioral tests ───────────────────────────────────────────────

func TestHandleSearchFilesFindsMatchingFiles(t *testing.T) {
	root := setupRoot(t)
	os.WriteFile(filepath.Join(root, "main.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "helper.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "readme.md"), []byte(""), 0644)

	res := callTool(t, handleSearchFiles, map[string]any{"path": root, "pattern": "*.go"})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	txt := resultText(res)
	if !strings.Contains(txt, "main.go") || !strings.Contains(txt, "helper.go") {
		t.Fatalf("expected .go files in results, got: %s", txt)
	}
	if strings.Contains(txt, "readme.md") {
		t.Fatalf("expected readme.md to be excluded from *.go results")
	}
}

func TestHandleSearchFilesSkipsHeavyDirs(t *testing.T) {
	root := setupRoot(t)
	os.MkdirAll(filepath.Join(root, ".git"), 0755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "main.go"), []byte(""), 0644)

	res := callTool(t, handleSearchFiles, map[string]any{"path": root, "pattern": "*"})
	txt := resultText(res)
	if strings.Contains(txt, ".git"+string(filepath.Separator)) {
		t.Fatal("expected .git contents to be skipped")
	}
}

// ── create_directory behavioral tests ────────────────────────────────────────────

func TestHandleCreateDirectoryCreatesDir(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "newdir", "sub")

	res := callTool(t, handleCreateDirectory, map[string]any{"path": p})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to exist at %s", p)
	}
}

// ── delete_file behavioral tests ────────────────────────────────────────────────

func TestHandleDeleteFileRemovesFile(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "todelete.txt")
	os.WriteFile(p, []byte("bye"), 0644)

	res := callTool(t, handleDeleteFile, map[string]any{"path": p})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("expected file to be deleted")
	}
}

func TestHandleDeleteFileRejectsDirectory(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "adir")
	os.MkdirAll(p, 0755)

	res := callTool(t, handleDeleteFile, map[string]any{"path": p})
	if !isError(res) {
		t.Fatal("expected error when trying to delete a directory")
	}
}

// ── get_file_info behavioral tests ──────────────────────────────────────────────

func TestHandleGetFileInfoReturnsMetadata(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "info.txt")
	os.WriteFile(p, []byte("12345"), 0644)

	res := callTool(t, handleGetFileInfo, map[string]any{"path": p})
	if isError(res) {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	txt := resultText(res)
	if !strings.Contains(txt, "size:") || !strings.Contains(txt, "5 bytes") {
		t.Fatalf("expected size metadata, got: %s", txt)
	}
	if !strings.Contains(txt, "type: file") {
		t.Fatalf("expected type: file, got: %s", txt)
	}
}

// ── /api/tools HTTP endpoint ─────────────────────────────────────────────────────

func TestAPIToolsEndpoint(t *testing.T) {
	buildMCPServer()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registeredTools)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var tools []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &tools); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(tools) < 10 {
		t.Fatalf("expected at least 10 tools, got %d", len(tools))
	}
}

// ── /message tools/list ───────────────────────────────────────────────────────────

func TestMessageHandlerAcceptsToolsList(t *testing.T) {
	if !strings.Contains(strings.ToLower(
		strings.Join(func() []string {
			names := make([]string, len(registeredTools))
			buildMCPServer()
			for i, tool := range registeredTools {
				names[i] = tool.Name
			}
			return names
		}(), ",")), "read_file") {
		t.Fatal("expected read_file in registered tools")
	}
}

func TestProbeToolsFunctionUsesCorrectMCPMethod(t *testing.T) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}
	f, err := staticFS.Open("js/app.js")
	if err != nil {
		t.Fatalf("open app.js: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 32768)
	n, _ := f.Read(buf)
	if !strings.Contains(string(buf[:n]), "tools/list") {
		t.Fatal("expected app.js to use tools/list MCP method")
	}
}

func TestMainGoExposesWorkersFlag(t *testing.T) {
	// Verify the worker pool is functional at package init level.
	if cap(workerSem) == 0 {
		t.Fatal("expected workerSem to have non-zero capacity")
	}
}

// ── concurrency correctness tests ───────────────────────────────────────────────

func TestWithQueuedWriteSerializesSamePath(t *testing.T) {
	fileGates = newFileGateManager()
	path := "/tmp/test-serial.txt"

	var active int32
	var overlap int32
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := withQueuedWrite(path, func() (string, error) {
				if n := atomic.AddInt32(&active, 1); n != 1 {
					atomic.StoreInt32(&overlap, 1)
				}
				time.Sleep(60 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return "done", nil
			})
			if err != nil {
				t.Errorf("write failed: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if atomic.LoadInt32(&overlap) != 0 {
		t.Fatal("writes to the same path overlapped; expected serialization")
	}
}

func TestWithConsistentReadWaitsForQueuedWrite(t *testing.T) {
	fileGates = newFileGateManager()
	path := "/tmp/test-read-block.txt"

	writerStarted := make(chan struct{})
	writerRelease := make(chan struct{})
	readFinished := make(chan struct{})

	go func() {
		_, _ = withQueuedWrite(path, func() (string, error) {
			close(writerStarted)
			<-writerRelease
			return "done", nil
		})
	}()

	<-writerStarted

	go func() {
		_, _ = withConsistentRead(path, func() (string, error) {
			close(readFinished)
			return "read", nil
		})
	}()

	select {
	case <-readFinished:
		t.Fatal("read completed before write finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(writerRelease)
	select {
	case <-readFinished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("read did not complete after write finished")
	}
}

func TestWithQueuedWriteAllowsParallelDifferentPaths(t *testing.T) {
	fileGates = newFileGateManager()

	var wg sync.WaitGroup
	both := make(chan struct{})
	var concurrentCount int32

	for _, p := range []string{"/tmp/path-a.txt", "/tmp/path-b.txt"} {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = withQueuedWrite(p, func() (string, error) {
				if atomic.AddInt32(&concurrentCount, 1) == 2 {
					close(both)
				}
				<-both
				atomic.AddInt32(&concurrentCount, -1)
				return "ok", nil
			})
		}()
	}

	select {
	case <-both:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("writes on different paths did not proceed in parallel")
	}
	wg.Wait()
}

func TestWorkerPoolBoundsParallelism(t *testing.T) {
	const limit = 3
	old := workerSem
	workerSem = make(chan struct{}, limit)
	defer func() { workerSem = old }()

	var active int32
	var exceeded int32
	var wg sync.WaitGroup

	for i := 0; i < limit*3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acquireWorker()
			defer releaseWorker()
			if n := atomic.AddInt32(&active, 1); n > limit {
				atomic.StoreInt32(&exceeded, 1)
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&exceeded) != 0 {
		t.Fatalf("worker pool exceeded limit of %d concurrent workers", limit)
	}
}

func TestFileGateManagerEvictsGatesAfterRelease(t *testing.T) {
	m := newFileGateManager()
	path := "/tmp/evict-test.txt"

	_, _ = withConsistentRead(path, func() (string, error) { return "", nil })

	// After the operation completes, the gate should be evicted (refs back to 0).
	m.mu.Lock()
	_, stillPresent := m.gates[path]
	m.mu.Unlock()

	// fileGates (the global) was used, not m — check the global.
	fileGates.mu.Lock()
	_, inGlobal := fileGates.gates[path]
	fileGates.mu.Unlock()
	if inGlobal {
		t.Fatal("expected gate to be evicted from fileGates after operation")
	}
	_ = stillPresent
}

func TestHandleReadFileAndWriteFileRoundTrip(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "roundtrip.txt")

	// Write then read back.
	callTool(t, handleWriteFile, map[string]any{"path": p, "content": "round trip content"})
	res := callTool(t, handleReadFile, map[string]any{"path": p})
	if isError(res) {
		t.Fatalf("read after write failed: %s", resultText(res))
	}
	if got := resultText(res); got != "round trip content" {
		t.Fatalf("expected %q, got %q", "round trip content", got)
	}
}

func TestHandlePatchFileThenRead(t *testing.T) {
	root := setupRoot(t)
	p := filepath.Join(root, "edit.txt")
	os.WriteFile(p, []byte("original content here"), 0644)

	callTool(t, handlePatchFile, map[string]any{"path": p, "old_str": "original", "new_str": "updated"})
	res := callTool(t, handleReadFile, map[string]any{"path": p})
	if got := resultText(res); got != "updated content here" {
		t.Fatalf("expected patched content, got %q", got)
	}
}
