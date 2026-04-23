package main

import (
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
)

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
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	ctype := w.Header().Get("Content-Type")
	expected := mime.TypeByExtension(".css")
	if expected == "" {
		expected = "text/css; charset=utf-8"
	}
	if !strings.Contains(ctype, "text/css") {
		t.Fatalf("expected CSS content type like %q, got %q", expected, ctype)
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
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	ctype := w.Header().Get("Content-Type")
	if !strings.Contains(ctype, "javascript") && !strings.Contains(ctype, "ecmascript") && !strings.Contains(ctype, "text/plain") {
		t.Fatalf("expected javascript-like content type, got %q", ctype)
	}
	if !strings.Contains(w.Body.String(), "probeTools") {
		t.Fatal("expected JS asset to contain probeTools function")
	}
}

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
	if len(loaded.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(loaded.Roots))
	}
	if loaded.Roots[0] != "/tmp/a" || loaded.Roots[1] != "/tmp/b" {
		t.Fatalf("unexpected roots: %#v", loaded.Roots)
	}
}

func TestSetRootsAndResolvePath(t *testing.T) {
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "a")
	rootB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(rootA, 0755); err != nil {
		t.Fatalf("mkdir rootA: %v", err)
	}
	if err := os.MkdirAll(rootB, 0755); err != nil {
		t.Fatalf("mkdir rootB: %v", err)
	}

	if err := setRoots([]string{rootA, rootB}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}

	resolved, err := resolvePath(filepath.Join(rootA, "child.txt"))
	if err != nil {
		t.Fatalf("resolvePath inside root: %v", err)
	}
	if !strings.HasPrefix(resolved, rootA) {
		t.Fatalf("expected resolved path under %q, got %q", rootA, resolved)
	}

	outside := filepath.Join(os.TempDir(), "outside-root.txt")
	if strings.HasPrefix(outside, rootA) || strings.HasPrefix(outside, rootB) {
		outside = filepath.Join(string(filepath.Separator), "definitely-outside-root.txt")
	}
	if _, err := resolvePath(outside); err == nil {
		t.Fatal("expected resolvePath to reject path outside allowed roots")
	}
}

func TestSetRootsRejectsMissingAndFilePaths(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "not-a-dir.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write filePath: %v", err)
	}

	if err := setRoots([]string{filepath.Join(tmp, "missing")}); err == nil {
		t.Fatal("expected setRoots to reject missing directory")
	}
	if err := setRoots([]string{filePath}); err == nil {
		t.Fatal("expected setRoots to reject non-directory path")
	}
}

func TestResolveProjectRootRequiresSessionMD(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := setRoots([]string{project}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}

	if _, err := resolveProjectRoot(project); err == nil {
		t.Fatal("expected resolveProjectRoot to require session.md")
	}

	sessionPath := filepath.Join(project, "session.md")
	if err := os.WriteFile(sessionPath, []byte("# session\n"), 0644); err != nil {
		t.Fatalf("write session.md: %v", err)
	}
	resolved, err := resolveProjectRoot(project)
	if err != nil {
		t.Fatalf("expected resolveProjectRoot to succeed once session.md exists: %v", err)
	}
	if resolved != project {
		t.Fatalf("expected resolved project root %q, got %q", project, resolved)
	}
}

func TestAppendSessionNoteAppendsContent(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	sessionPath := filepath.Join(project, "session.md")
	if err := os.WriteFile(sessionPath, []byte("# session\n"), 0644); err != nil {
		t.Fatalf("write session.md: %v", err)
	}
	if err := setRoots([]string{project}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}

	msg, err := appendSessionNote(project, "Did a thing", "Do the next thing")
	if err != nil {
		t.Fatalf("appendSessionNote: %v", err)
	}
	if !strings.Contains(msg, "updated") {
		t.Fatalf("expected update message, got %q", msg)
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Did a thing") {
		t.Fatal("expected session.md to contain summary text")
	}
	if !strings.Contains(text, "Do the next thing") {
		t.Fatal("expected session.md to contain next steps text")
	}
}

func TestRunMakeTargetRejectsProjectWithoutSessionMD(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := setRoots([]string{project}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}

	if _, err := runMakeTarget(project, "test"); err == nil {
		t.Fatal("expected runMakeTarget to reject project without session.md")
	}
}

func TestBuildMCPServerRegistersCoreReadWriteTools(t *testing.T) {
	s := buildMCPServer()
	if s == nil {
		t.Fatal("expected buildMCPServer to return a server")
	}

	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)
	for _, tool := range []string{"read_file", "write_file", "patch_file"} {
		needle := "mcp.NewTool(\"" + tool + "\""
		if !strings.Contains(text, needle) {
			t.Fatalf("expected main.go to register tool %q", tool)
		}
	}
}

func TestReadFileToolImplementationUsesResolvePathAndOSReadFile(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	if !strings.Contains(text, "mcp.NewTool(\"read_file\"") {
		t.Fatal("expected read_file tool registration in main.go")
	}
	if !strings.Contains(text, "resolvePath(req.GetString(\"path\", \"\"))") {
		t.Fatal("expected read_file to resolve paths through resolvePath")
	}
	if !strings.Contains(text, "os.ReadFile(abs)") {
		t.Fatal("expected read_file implementation to use os.ReadFile(abs)")
	}
}

func TestWriteFileToolImplementationCreatesParentDirsAndWritesFile(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	if !strings.Contains(text, "mcp.NewTool(\"write_file\"") {
		t.Fatal("expected write_file tool registration in main.go")
	}
	if !strings.Contains(text, "os.MkdirAll(filepath.Dir(abs), 0755)") {
		t.Fatal("expected write_file to create parent directories")
	}
	if !strings.Contains(text, "os.WriteFile(abs, []byte(content), 0644)") {
		t.Fatal("expected write_file implementation to write file contents")
	}
}

func TestPatchFileToolImplementationReadsReplacesAndWrites(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	if !strings.Contains(text, "mcp.NewTool(\"patch_file\"") {
		t.Fatal("expected patch_file tool registration in main.go")
	}
	if !strings.Contains(text, "strings.Replace(src, oldStr") {
		t.Fatal("expected patch_file to replace oldStr in the source")
	}
	if !strings.Contains(text, "os.WriteFile(abs, []byte(updated), 0644)") {
		t.Fatal("expected patch_file implementation to write updated contents")
	}
}

func TestListAndSearchToolImplementationsRetainDirectorySafety(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, tool := range []string{"list_directory", "search_files", "get_file_info"} {
		needle := "mcp.NewTool(\"" + tool + "\""
		if !strings.Contains(text, needle) {
			t.Fatalf("expected main.go to register tool %q", tool)
		}
	}

	if !strings.Contains(text, "os.ReadDir(abs)") {
		t.Fatal("expected list_directory implementation to use os.ReadDir(abs)")
	}
	if !strings.Contains(text, "filepath.WalkDir(abs") {
		t.Fatal("expected search_files implementation to walk directories from the resolved root")
	}
	if !strings.Contains(text, "skipDirs[d.Name()]") {
		t.Fatal("expected directory traversal to respect skipDirs entries")
	}
	if !strings.Contains(text, "os.Stat(abs)") {
		t.Fatal("expected get_file_info implementation to stat the resolved path")
	}
}

func TestSkipDirsIncludesCommonHeavyDirectories(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "vendor", ".venv", "dist", "build", ".next", ".cache"} {
		if !skipDirs[name] {
			t.Fatalf("expected skipDirs to include %q", name)
		}
	}
}

func TestResolvePathAllowsExactRootAndNestedPaths(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatalf("mkdir nested root: %v", err)
	}
	if err := setRoots([]string{root}); err != nil {
		t.Fatalf("setRoots: %v", err)
	}

	resolvedRoot, err := resolvePath(root)
	if err != nil {
		t.Fatalf("resolvePath exact root: %v", err)
	}
	if resolvedRoot != root {
		t.Fatalf("expected exact root %q, got %q", root, resolvedRoot)
	}

	nested := filepath.Join(root, "nested", "file.txt")
	resolvedNested, err := resolvePath(nested)
	if err != nil {
		t.Fatalf("resolvePath nested path: %v", err)
	}
	if resolvedNested != nested {
		t.Fatalf("expected nested path %q, got %q", nested, resolvedNested)
	}
}

func TestBuildMCPServerRegistersReadFilesTool(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)
	if !strings.Contains(text, `mcp.NewTool("read_files"`) {
		t.Fatal("expected read_files tool registration in main.go")
	}
}

func TestReadFilesToolImplementationUsesArrayArgumentAndResolvePath(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	if !strings.Contains(text, `mcp.NewTool("read_files"`) {
		t.Fatal("expected read_files tool registration in main.go")
	}
	if !strings.Contains(text, `mcp.WithArray("paths"`) {
		t.Fatal("expected read_files to declare an array argument named paths")
	}
	if !strings.Contains(text, `resolvePath(path)`) {
		t.Fatal("expected read_files implementation to resolve each requested path")
	}
	if !strings.Contains(text, `os.ReadFile(abs)`) {
		t.Fatal("expected read_files implementation to read each resolved file")
	}
}

func TestReadFilesToolAggregatesResolvedFilesWithHeadings(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`paths must be a non-empty array`,
		`paths entries must be non-empty strings`,
		`==> `,
		` <==\n`,
		`logger.Printf("read_files: %d files", len(paths))`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected read_files implementation marker %q in main.go", needle)
		}
	}
}

func TestMainGoDefinesConfigurableWorkerLimitAndFileGateTypes(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`type fileGate struct`,
		`type fileGateManager struct`,
		`func newFileGateManager()`,
		`var fileGates`,
		`maxWorkers`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected concurrency scaffold marker %q in main.go", needle)
		}
	}
}

func TestMainGoDefinesQueuedWriteAndConsistentReadHelpers(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`func withQueuedWrite(`,
		`func withConsistentRead(`,
		`pendingWrites`,
		`sync.Cond`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected concurrency helper marker %q in main.go", needle)
		}
	}
}

func TestWithQueuedWriteSerializesSamePath(t *testing.T) {
	fileGates = newFileGateManager()
	path := "/tmp/test-serial.txt"

	var active int32
	var overlap int32
	var wg sync.WaitGroup
	wg.Add(2)

	start := make(chan struct{})

	go func() {
		defer wg.Done()
		<-start
		_, err := withQueuedWrite(path, func() (string, error) {
			if n := atomic.AddInt32(&active, 1); n != 1 {
				atomic.StoreInt32(&overlap, 1)
			}
			time.Sleep(80 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return "first", nil
		})
		if err != nil {
			t.Errorf("first write failed: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		_, err := withQueuedWrite(path, func() (string, error) {
			if n := atomic.AddInt32(&active, 1); n != 1 {
				atomic.StoreInt32(&overlap, 1)
			}
			time.Sleep(80 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return "second", nil
		})
		if err != nil {
			t.Errorf("second write failed: %v", err)
		}
	}()

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
		_, err := withQueuedWrite(path, func() (string, error) {
			close(writerStarted)
			<-writerRelease
			return "done", nil
		})
		if err != nil {
			t.Errorf("write failed: %v", err)
		}
	}()

	<-writerStarted

	go func() {
		_, err := withConsistentRead(path, func() (string, error) {
			close(readFinished)
			return "read", nil
		})
		if err != nil {
			t.Errorf("read failed: %v", err)
		}
	}()

	select {
	case <-readFinished:
		t.Fatal("read completed before queued write finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(writerRelease)

	select {
	case <-readFinished:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("read did not complete after queued write finished")
	}
}

func TestWithQueuedWriteAllowsParallelDifferentPaths(t *testing.T) {
	fileGates = newFileGateManager()

	var active int32
	var maxActive int32
	var wg sync.WaitGroup
	wg.Add(2)

	start := make(chan struct{})

	runWrite := func(path string) {
		defer wg.Done()
		<-start
		_, err := withQueuedWrite(path, func() (string, error) {
			n := atomic.AddInt32(&active, 1)
			for {
				currentMax := atomic.LoadInt32(&maxActive)
				if n <= currentMax || atomic.CompareAndSwapInt32(&maxActive, currentMax, n) {
					break
				}
			}
			time.Sleep(80 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return path, nil
		})
		if err != nil {
			t.Errorf("write failed for %s: %v", path, err)
		}
	}

	go runWrite("/tmp/path-a.txt")
	go runWrite("/tmp/path-b.txt")

	close(start)
	wg.Wait()

	if atomic.LoadInt32(&maxActive) < 2 {
		t.Fatal("writes on different paths did not run in parallel")
	}
}

func TestReadAndWriteToolsUseConcurrencyHelpers(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`withConsistentRead(abs, func() (string, error) {`,
		`withQueuedWrite(abs, func() (string, error) {`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected concurrency helper usage marker %q in main.go", needle)
		}
	}
}

func TestMainGoDefinesWorkerPoolAndSemaphore(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`workerSem`,
		`make(chan struct{}, maxWorkers)`,
		`func acquireWorker(`,
		`func releaseWorker(`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected worker pool marker %q in main.go", needle)
		}
	}
}

func TestFileToolsAcquireAndReleaseWorker(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	if !strings.Contains(text, `acquireWorker()`) {
		t.Fatal("expected file tools to call acquireWorker() before doing work")
	}
	if !strings.Contains(text, `defer releaseWorker()`) {
		t.Fatal("expected file tools to defer releaseWorker() to ensure release")
	}
}

func TestWorkerPoolBoundsParallelism(t *testing.T) {
	oldMax := maxWorkers
	maxWorkers = 2
	workerSem = make(chan struct{}, maxWorkers)
	defer func() {
		maxWorkers = oldMax
		workerSem = make(chan struct{}, maxWorkers)
	}()

	var active int32
	var exceeded int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acquireWorker()
			defer releaseWorker()
			n := atomic.AddInt32(&active, 1)
			if n > int32(maxWorkers) {
				atomic.StoreInt32(&exceeded, 1)
			}
			time.Sleep(40 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if atomic.LoadInt32(&exceeded) != 0 {
		t.Fatal("worker pool exceeded maxWorkers concurrent workers")
	}
}

func TestMainGoExposesWorkersFlag(t *testing.T) {
	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`flag.IntVar(`,
		`workers`,
		`workerSem = make(chan struct{}, maxWorkers)`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected workers flag marker %q in main.go", needle)
		}
	}
}

func TestMessageHandlerAcceptsToolsList(t *testing.T) {
	s := buildMCPServer()
	if s == nil {
		t.Fatal("expected buildMCPServer to return a server")
	}

	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)

	for _, needle := range []string{
		`"/message"`,
		`tools/list`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected /message handler marker %q in main.go", needle)
		}
	}
}

func TestProbeToolsFunctionUsesCorrectMCPMethod(t *testing.T) {
	appJS, err := os.ReadFile("static/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	text := string(appJS)

	if strings.Contains(text, `'list_tools'`) || strings.Contains(text, `"list_tools"`) {
		t.Fatal("app.js uses incorrect MCP method 'list_tools'; should use 'tools/list'")
	}
	if !strings.Contains(text, `tools/list`) {
		t.Fatal("expected app.js probeTools to use MCP method 'tools/list'")
	}
	if strings.Contains(text, `JSON.stringify(payload)`) && strings.Contains(text, `payload = [{`) {
		t.Fatal("app.js wraps JSON-RPC payload in an array; should send a single object")
	}
}

func TestBuildMCPServerIncludesCoreTools(t *testing.T) {
	s := buildMCPServer()
	if s == nil {
		t.Fatal("expected buildMCPServer to return a server")
	}

	mainGo, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(mainGo)
	for _, tool := range []string{"read_file", "write_file", "patch_file", "list_directory", "search_files", "create_directory", "delete_file", "get_file_info", "update_session_md", "make_test", "make_build"} {
		needle := "mcp.NewTool(\"" + tool + "\""
		if !strings.Contains(text, needle) {
			t.Fatalf("expected main.go to register tool %q", tool)
		}
	}
}
